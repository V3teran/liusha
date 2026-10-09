// Package main 是 liusha api 进程入口：装配 config / pg / redis / stores / httpapi。
//
// 启动顺序：logger → config（含 ENV 覆盖）→ pg+redis → stores → http server → 监听 SIGINT/SIGTERM。
// 关闭顺序：收到信号后用 5s 超时 ctx 调 srv.Shutdown，再让 defer 关 pool/redis。
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	agent "github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	llmcfg "github.com/V3teran/liusha/internal/config/llm"
	"github.com/V3teran/liusha/internal/config/seed"
	"github.com/V3teran/liusha/internal/config/setting"
	cfgtool "github.com/V3teran/liusha/internal/config/tool"
	cfgstore "github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
)

func main() {
	logger := logx.New("api")
	ctx := context.Background()

	cfg, err := config.Load(envx.OrDefault("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}

	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"),
		cfg.Postgres.MaxConns, cfg.Postgres.MinConns,
		cfg.Postgres.ConnectTimeoutSeconds, cfg.Postgres.MaxConnLifetimeSeconds)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	rdb, err := db.NewRedis(ctx, os.Getenv("LIUSHA_REDIS_ADDR"), cfg.Redis)
	if err != nil {
		logger.Fatal().Err(err).Msg("redis")
	}
	defer func() { _ = rdb.Close() }()

	credAPI := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	taskStore := task.NewStore(pool)
	assignmentStore := assignment.NewStore(pool)
	cronStore := cronschedule.NewStore(pool) // 定时模板（§3.3/§4.2），Scheduler goroutine 轮询
	findStore := finding.NewStore(pool)
	proxyTrafficStore := traffic.NewProxyStore(pool) // cron 定时触发 passive 展开时领取该 host 未消费流量
	invocationStore := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	defer func() { _ = invocationStore.Close() }()
	controlPlaneStore := controlplane.NewStore(pool) // 任务控制平面（人工干预）

	// agent run store + asynq 入队器。
	agentRunStore := agentrun.NewStore(pool)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")})
	defer func() { _ = enq.Close() }()

	// 共享多级缓存内核（L1 内存 + L2 redis + 跨进程失效总线）。所有配置资源
	// （agent，后续 llm/system）复用同一实例；一条 Subscribe 循环覆盖全部资源。
	// Subscribe 阻塞运行（内部 for-select 直到 ctx 取消），必须后台起——
	// 同步调用会把 main goroutine 卡死在订阅循环。
	cache := cachestore.New(rdb, 0)
	go func() {
		if err := cache.Subscribe(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("cachestore 失效订阅退出——配置跨进程失效不可用")
		}
	}()

	// 配置多级缓存 Store（agent CRUD 后端）。写路径经 cachestore 广播失效，
	// runner 进程被动失效其 L1。
	cfgStore := cfgstore.New(pool, cache)

	// LLM 配置多级缓存 Store（provider 部署 / 别名 / 角色路由）。既是「模型模块」CRUD 后端，
	// 又是两个 LLM 工厂运行期 role→provider 解析的事实源（复用同一 cache 实例）。
	cfgAgentStore := agent.NewStore(pool)
	cfgSkillStoreRaw := skillstore.NewStore(pool) // 种子加载用（裸 DB 写）
	// agent.complexity 的消费在 runner cognition（AgentByCode 三级缓存直读直传 router.For），
	// 不经 llmstore role 路由，故此处无需 complexity override 装配。
	llmStore := llmstore.New(pool, cache)

	// 系统业务旋钮 Store（compaction/runtime/proxy_filter 三组）。既是「系统配置」CRUD 后端，
	// 又是 runner 现读 / proxy 热换过滤链的事实源（复用同一 cache 实例——写后失效广播即时可见）。
	settingStore := settingstore.New(pool, cache)

	// 种子首填（insert-only）：空库时从磁盘 agents/ 和 skills/ 导入默认配置，
	// 已存在的行按 code 整行跳过（DB 是事实源，不覆盖运维/前端改动）；
	// 同时清理文件里已删除的内置 skill 死行（目录改名/删除残留）。
	// 目录缺失时静默跳过（walkFiles 容忍不存在），非致命——失败仅告警不 fail-fast，
	// 让 api 仍能起（配置可事后经 CRUD 补齐）。
	seedDir := envx.OrDefault("LIUSHA_SEED_DIR", ".")
	seedRes, err := seed.Import(ctx, seedDir, cfgAgentStore, cfgSkillStoreRaw)
	if err != nil {
		logger.Warn().Err(err).Str("dir", seedDir).Msg("配置种子导入失败（跳过，可经 CRUD 手动补齐）")
	} else if len(seedRes.Agents) > 0 || len(seedRes.Skills.Touched()) > 0 {
		// 种子写动了 DB（新行/死行清理）→ 失效多级缓存并广播（本进程缓存虽冷，
		// 但 runner/其他 api 实例的 L1/L2 可能已有旧值）。
		if invErr := cfgStore.InvalidateSkills(ctx, seedRes.Skills.Touched()...); invErr != nil {
			logger.Warn().Err(invErr).Msg("种子写入后 skill 缓存失效失败（10min L2 TTL 兜底）")
		}
		if invErr := cfgStore.InvalidateAgents(ctx, seedRes.Agents...); invErr != nil {
			logger.Warn().Err(invErr).Msg("种子写入后 agent 缓存失效失败（10min L2 TTL 兜底）")
		}
		logger.Info().
			Strs("agents_seeded", seedRes.Agents).
			Strs("skills_inserted", seedRes.Skills.Inserted).
			Strs("skills_pruned", seedRes.Skills.Pruned).
			Msg("配置种子首填完成")
	}

	// LLM 配置种子（insert-only）：把 config.yaml 的 providers:/llm.* 首填进
	// llm_provider/alias/role_route，空库时建默认路由。已存在的行整行跳过（DB 事实源）。
	if err := seed.ImportLLM(ctx, cfg, llmcfg.NewStore(pool)); err != nil {
		logger.Warn().Err(err).Msg("LLM 配置种子导入失败（跳过，可经模型模块 CRUD 手动补齐）")
	}

	// 系统业务旋钮种子（insert-only）：把 config.yaml 的 compaction/runtime/proxy_filter 三组
	// 首填进 system_setting，空库时建默认旋钮。已存在的组整组跳过（DB 事实源）。
	// 复用同一 cache 实例——写后经失效总线广播，runner/proxy 立即读到最新旋钮。
	if err := seed.ImportSystem(ctx, cfg, settingStore); err != nil {
		logger.Warn().Err(err).Msg("系统配置种子导入失败（跳过，可经系统配置 CRUD 手动补齐）")
	}

	// Tools manifest（tools.yaml）：供 GET /tooling/tools 给 AgentAdmin cli_tools 白名单多选器
	// 拉取候选。与 runner 同源加载；缺失非致命（仅该只读端点不注册，配置页 cli_tools 候选为空）。
	toolsManifestPath := envx.OrDefault("LIUSHA_TOOLS_MANIFEST_PATH", "deployments/tool-images/pentools/tools.yaml")
	var toolManifest *manifest.Manifest // cli 工具事实源；nil = tools.yaml 缺失（降级：仅同步 function 工具）
	if m, mErr := manifest.Load(toolsManifestPath); mErr != nil {
		logger.Warn().Err(mErr).Str("path", toolsManifestPath).Msg("tools.yaml 加载失败（工具目录仅同步内置 function 工具）")
	} else {
		toolManifest = m
	}

	// 工具目录同步：把两套工具体系（内置 function + 外置 cli）幂等同步进 tool 表，
	// 供前端工具模块检索/展示、智能体配置页「选工具」。代码为事实源，启动期 reconcile 一次。
	cfgToolStore := cfgtool.NewStore(pool)
	if up, pr, rErr := cfgtool.Reconcile(ctx, cfgToolStore, toolManifest); rErr != nil {
		logger.Warn().Err(rErr).Msg("工具目录同步失败（工具模块可能展示陈旧目录）")
	} else {
		logger.Info().Int("upserted", up).Int("pruned", pr).Msg("工具目录已同步")
	}

	// LLM provider API Key 加密密钥（migration 0103）：32 字节 hex 编码，AES-256-GCM。
	// 缺失/长度不对直接拒启动——前端「LLM 配置」页写密钥、两个 LLM 工厂读密钥都靠它，
	// 静默跳过会导致密钥落库变成明文或运行期解密报错，不如 fail-fast 在启动期截住。
	llmKeyCipher, err := cryptx.NewFromEnv("LIUSHA_LLM_KEY_SECRET")
	if err != nil {
		logger.Fatal().Err(err).Msg("LIUSHA_LLM_KEY_SECRET 未配置或不合法——provider 密钥加密需要它（fail-fast）")
	}

	auditStore := audit.NewStore(pool)         // 0047：task abort / create 审计
	convStore := conversation.NewStore(pool)   // 阶段B：会话/消息
	toolStore := toolinvocation.NewStore(pool) // 会话用量合计：工具耗时来源

	// Phase 1: 探索图 API（e2e 测试迁移专用）
	explorationGraphAdapter := explorationgraph.NewStore(pool)

	// 多轮问答/意图分类依赖：light provider 路由 + 问答读 finding + SSE publish。
	// llmKeyCipher 解密 provider 的加密密钥（migration 0103），构造 client 前才解密，不进缓存。
	router := llm.NewRouterWithOptions(llm.NewFactory(llmStore, llmKeyCipher), llm.RetryOptionsFromConfig(cfg.LLM.Retry))
	publisher := scanstream.NewPublisher(rdb)
	adapter := &scanAdapter{assignments: assignmentStore, tasks: taskStore, agentRuns: agentRunStore, enq: enq, audit: auditStore, conversations: convStore, router: router, findings: findStore, publisher: publisher, maxRunTimeout: time.Duration(cfg.Runner.AgentRunTimeoutSeconds) * time.Second}

	// provider 实连探测（前端「LLM 配置」页「测试连接」+ 模型下拉探测）：闭合 llmStore（取已存密钥走
	// 多级缓存）+ llmKeyCipher（解密）+ 独立 ClientPool（不与 router 内部池耦合，探测是低频交互路径）。
	providerTester := newProviderTester(llmStore, llmKeyCipher, llm.NewClientPool())

	// cron Scheduler（§4.2/§10 P4）：轮询 cron_schedule 到点模板 → 克隆 assignment → 展开 task。
	// 单副本够用；ctx 随进程关停取消（无需独立 shutdown 时限——轮询循环立即退出，无 in-flight 状态要收尾）。
	cronCtx, cronCancel := context.WithCancel(context.Background())
	defer cronCancel()
	runner := &cronRunner{
		schedules:   cronStore,
		assignments: assignmentStore,
		tasks:       taskStore,
		proxyStore:  proxyTrafficStore,
		agentRuns:   agentRunStore,
		enq:         enq,
		scan:        adapter,
		logger:      logger,
	}
	go runner.run(cronCtx)

	// SSE stream cookie 密钥：会话功能开启时必填（EventSource 鉴权用），缺失 fail-fast。
	streamSecret := []byte(os.Getenv("LIUSHA_STREAM_COOKIE_SECRET"))
	if len(streamSecret) == 0 {
		logger.Fatal().Msg("LIUSHA_STREAM_COOKIE_SECRET 未配置——SSE stream cookie 鉴权需要它（fail-fast）")
	}

	// 监听地址：优先 ENV（运维临时切换）→ yaml。
	listenAddr := envx.OrDefault("LIUSHA_API_ADDR", cfg.API.ListenAddr)
	srv := &http.Server{
		Addr: listenAddr,
		Handler: httpapi.NewServer(httpapi.Deps{
			APIKey:             os.Getenv("LIUSHA_API_KEY"),
			StreamCookieSecret: streamSecret,
			CookieSecure:       os.Getenv("LIUSHA_COOKIE_SECURE") == "true",
			Credentials:        credAPI,
			Tasks: taskAPIAdapter{
				tasks: taskStore,
				audit: auditStore,
			},
			Findings:          findStore, // 全局漏洞台账（active+passive 全量 + triage 处置）
			Invocations:       invocationStore,
			Scan:              adapter,
			Chat:              adapter,                      // 阶段B：POST /chat 会话发起扫描
			FollowUp:          adapter,                      // 多轮：POST /conversations/:id/messages 动作续接
			Abort:             adapter,                      // 多轮：POST /conversations/:id/abort 停止会话关联扫描
			Deleter:           adapter,                      // DELETE /conversations/:id 删会话+消息；关联扫描进行中拒删（409，先停后删）
			Renamer:           convStore,                    // PATCH /conversations/:id 重命名标题（convStore.SetTitle 直接满足）
			ConfigStore:       cfgStore,                     // agent 配置 CRUD（配置管理页）
			SkillStore:        cfgStore,                     // skill 配置 CRUD（知识库管理页），走多级缓存
			ToolCatalog:       cfgToolStore,                 // GET /tools、/tools/:name：工具目录检索/详情 + 智能体选工具
			Models:            llmStore,                     // GET/POST/PUT/DELETE /models：provider 部署 CRUD + 角色路由面板
			KeyEncrypter:      llmKeyCipher,                 // POST/PUT /models/providers：加密前端直填的明文 API Key
			ProviderTester:    providerTester,               // POST /models/providers/test|list-models：实连探测（不落库）
			Settings:          settingStore,                 // GET/PUT /settings 系列：compaction/runtime/proxy_filter 三组业务旋钮
			Traffic:           proxyTrafficStore,            // GET /traffic 系列：代理捕获流量只读浏览（流量模块）
			TrafficConv:       convStore,                    // 流量详情消费关系 chip → task 反解会话 id 跳转
			Conversations:     convStore,                    // 阶段B：会话列表 / 消息回看
			EventStream:       eventStreamAdapter{rdb: rdb}, // 阶段B：SSE 订阅 redis 事件
			UsageTasks:        convStore,                    // 会话用量：会话→task 解析
			UsageLLM:          invocationStore,              // 会话用量：LLM token/耗时合计
			UsageTools:        toolStore,                    // 会话用量：工具耗时合计
			ControlPlane:      controlPlaneStore,            // 任务控制平面（人工干预）
			ExplorationGraph:  explorationGraphAdapter,      // Phase 1: 探索图 API（e2e 测试迁移）
			EnableDevAutofill: envx.OrDefault("LIUSHA_DEV_AUTOFILL", "") != "",
		}),
		ReadTimeout:  time.Duration(cfg.API.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.API.WriteTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info().Str("addr", srv.Addr).Msg("api listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("api serve")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	logger.Info().Str("signal", sig.String()).Msg("api shutting down")

	shutdownTimeout := time.Duration(cfg.API.ShutdownTimeoutSeconds) * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("api shutdown")
	}
	logger.Info().Msg("api stopped")
}
