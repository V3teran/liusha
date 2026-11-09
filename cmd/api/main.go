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

	"github.com/rs/zerolog"

	agent "github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/attachment"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/auth"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/seed"
	configstore "github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/dispatch"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/httpapi"
	llmconfig "github.com/V3teran/liusha/internal/llmconfig"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/llmprobe"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/settingstore"
	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/toolstore"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/V3teran/liusha/internal/workspace"

	"github.com/hibiken/asynq"
)

func main() {
	logger := logx.New("api")
	ctx := context.Background()

	cfg, err := config.Load(envx.OrDefault("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}

	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"), logger,
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
	proxyTrafficStore := traffic.NewProxyStore(pool) // 代理捕获流量只读浏览（流量模块）
	invocationStore := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	defer func() { _ = invocationStore.Close() }()
	controlPlaneStore := controlplane.NewStore(pool) // 任务控制平面（人工干预）

	// agent run store + asynq 入队器。
	agentRunStore := agentrun.NewStore(pool)
	// 密码 env 显式读取（LIUSHA_REDIS_PASSWORD）——redis 强制密码后 asynq 入队
	// 若不带会 NOAUTH（SADD 幂等键写入即失败）。
	enq := worker.NewClient(asynq.RedisClientOpt{
		Addr:     os.Getenv("LIUSHA_REDIS_ADDR"),
		Password: os.Getenv("LIUSHA_REDIS_PASSWORD"),
	})
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
	cfgStore := configstore.New(pool, cache)

	// 启动自检：验证所有 agent 的 system_prompt 已正确加载
	// （在 seed.Import 之后验证，确保种子已导入）
	// 注意：这里延迟到 seed.Import 之后再验证
	validateAgentConfigs := func() {
		validateCtx, validateCancel := context.WithTimeout(ctx, 10*time.Second)
		defer validateCancel()
		if err := cfgStore.ValidateAgentConfigs(validateCtx); err != nil {
			logger.Fatal().Err(err).Msg("Agent 配置验证失败")
		}
		logger.Info().Msg("Agent 配置验证通过（executor/planner/evaluator/monitor）")
	}

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
	seedRes, err := seed.Import(ctx, cfgAgentStore, cfgSkillStoreRaw)
	if err != nil {
		logger.Warn().Err(err).Msg("配置种子导入失败（跳过，可经 CRUD 手动补齐）")
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

	// 种子导入完成后，验证 agent 配置
	validateAgentConfigs()

	// LLM 配置种子（仅首次启动）：llm_provider 空表时才把 config.yaml 的 providers:/llm.*
	// 导入建默认路由；非空库整段跳过（DB 是运行期事实源，UI 删除不被 yaml 复活）。
	if err := seed.ImportLLM(ctx, cfg, llmconfig.NewStore(pool)); err != nil {
		logger.Warn().Err(err).Msg("LLM 配置种子导入失败（跳过，可经模型模块 CRUD 手动补齐）")
	}
	// seed 直连 DB 写，绕过 llmstore 缓存层：导入后清空 LLM 配置读缓存，
	// 否则 redis 里的旧值（如导入前缓存过的空路由）会盖过刚重建的配置。
	if err := llmStore.InvalidateAll(ctx); err != nil {
		logger.Warn().Err(err).Msg("LLM 配置缓存清空失败（运行期首读可能短暂滞后）")
	}
	// 零 provider 不拒启：模型配置允许「先部署、后在前端配置」。自洽由三层保证——
	// 下发/定时模板创建时边界拒绝（dispatch.requireLLMReady，400 带配置指引）、
	// 运行期未配置错误致命化（llm.ErrNotConfigured → 任务干净终结不再永挂）、
	// cron 到点触发跳过待下轮。这里只做可见性告警。
	if provs, provErr := llmconfig.NewStore(pool).ListProviders(ctx, false); provErr != nil {
		logger.Warn().Err(provErr).Msg("读取 LLM provider 失败（任务下发前请检查 LLM 配置）")
	} else if len(provs) == 0 {
		logger.Warn().Msg("LLM 未配置：尚无任何 provider——服务可启动，任务下发会被拒绝；请在「LLM 配置」页接入 provider")
	}

	// 系统业务旋钮种子（insert-only）：把 config.yaml 的 compaction/runtime/proxy_filter 三组
	// 首填进 system_setting，空库时建默认旋钮。已存在的组整组跳过（DB 事实源）。
	// 复用同一 cache 实例——写后经失效总线广播，runner/proxy 立即读到最新旋钮。
	if err := seed.ImportSystem(ctx, cfg, settingStore); err != nil {
		logger.Warn().Err(err).Msg("系统配置种子导入失败（跳过，可经系统配置 CRUD 手动补齐）")
	}

	// Tools manifest（tools.yaml）：供 GET /tooling/tools 给 AgentAdmin cli_tools 白名单多选器
	// 拉取候选。与 runner 同源加载；缺失非致命（仅该只读端点不注册，配置页 cli_tools 候选为空）。
	toolsManifestPath := envx.OrDefault("LIUSHA_TOOLS_MANIFEST_PATH", "deployments/tool-images/sandbox/tools.yaml")
	var toolManifest *manifest.ToolManifest // cli 工具事实源；nil = tools.yaml 缺失（降级：仅同步 function 工具）
	if m, mErr := manifest.Load(toolsManifestPath); mErr != nil {
		logger.Warn().Err(mErr).Str("path", toolsManifestPath).Msg("tools.yaml 加载失败（工具目录仅同步内置 function 工具）")
	} else {
		toolManifest = m
	}

	// 工具目录同步：把两套工具体系（内置 function + 外置 cli）幂等同步进 tool 表，
	// 供前端工具模块检索/展示、智能体配置页「选工具」。代码为事实源，启动期 reconcile 一次。
	toolCatalog := toolstore.NewStore(pool)
	if up, pr, rErr := toolstore.Reconcile(ctx, toolCatalog, toolManifest); rErr != nil {
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

	auditStore := audit.NewStore(pool)               // 0047：task abort / create 审计
	conversationStore := conversation.NewStore(pool) // 会话/消息
	toolStore := toolinvocation.NewStore(pool)       // 会话用量合计：工具耗时来源

	// 日志查看（「日志」页）：api 尾读共享日志卷上各进程的 logs/{process}.log。
	// dir 与写入端同一解析（LIUSHA_LOG_DIR）——单容器/共享卷部署三进程文件全见。
	logReader := logx.NewReader(logx.Dir())

	// Phase 1: 探索图 API（e2e 测试迁移专用）
	explorationGraphAdapter := explorationgraph.NewStore(pool)

	// 多轮问答/意图分类依赖：light provider 路由 + 问答读 finding + SSE publish。
	// llmKeyCipher 解密 provider 的加密密钥（migration 0103），构造 client 前才解密，不进缓存。
	// 与 runner 同一埋点口径：单步看门狗（现读 runtime.step_llm_timeout_seconds 热配置）
	// + llm_invocation 审计（chat/qa/意图分类无 run 归属，落 NULL 外键 + Role 标签）。
	stepLLMTimeout := time.Duration(cfg.Runner.StepLLMTimeoutSeconds) * time.Second
	apiLLMRouter := fwllm.NewRouterWithOpts(
		llmStore.AsRouterStore(llmKeyCipher),
		retryOptionsFromConfig(cfg.LLM.Retry))
	apiLLMRouter.SetProviderWrapper(func(p fwllm.Provider) fwllm.Provider {
		return fwllm.InstrumentProvider(fwllm.WithDynamicCallTimeout(p,
			stepLLMTimeout,
			func(ctx context.Context) time.Duration {
				rt, err := settingStore.Runtime(ctx)
				if err != nil {
					return 0
				}
				return time.Duration(rt.StepLLMTimeoutSeconds) * time.Second
			}), llminvocation.UsageSink{Store: invocationStore})
	})
	router := &dispatch.RoleRouter{Router: apiLLMRouter}
	publisher := convstream.NewPublisher(rdb)
	dispatchService := &dispatch.Service{
		LLM:           llmReadiness{apiLLMRouter},
		Assignments:   assignmentStore,
		Tasks:         taskStore,
		AgentRuns:     agentRunStore,
		Enq:           enq,
		Audit:         auditStore,
		Conversations: conversationStore,
		Schedules:     cronStore,
		Router:        router,
		Findings:      findStore,
		Publisher:     publisher,
		Control:       controlPlaneStore,
		MaxRunTimeout: time.Duration(cfg.Runner.AgentRunTimeoutSeconds) * time.Second,
	}

	// provider 实连探测（前端「LLM 配置」页「测试连接」+ 模型下拉探测）：闭合 llmStore（取已存密钥走
	// 多级缓存）+ llmKeyCipher（解密）+ 独立 ClientPool（不与 router 内部池耦合，探测是低频交互路径）。
	providerTester := llmprobe.New(llmStore, llmKeyCipher, fwllm.NewClientPool())

	// cron Scheduler：轮询 cron_schedule 到点模板 → 克隆 assignment → 展开 task。
	// 单副本够用；ctx 随进程关停取消（无需独立 shutdown 时限——轮询循环立即退出，无 in-flight 状态要收尾）。
	cronCtx, cronCancel := context.WithCancel(context.Background())
	defer cronCancel()
	scheduler := &dispatch.Scheduler{
		Schedules:     cronStore,
		Assignments:   assignmentStore,
		Tasks:         taskStore,
		AgentRuns:     agentRunStore,
		Enq:           enq,
		Dispatcher:    dispatchService,
		Conversations: conversationStore, // cron 代理捕获 task 建会话流（与实时聚合路径同语义）
		Logger:        logger,
	}
	go scheduler.Run(cronCtx)

	// 登录认证：账号/会话 store（account / account_session 表）+ 审计出口。
	authStore := auth.NewStore(pool)
	ensureInitialAdmin(ctx, authStore, logger)

	// 监听地址：优先 ENV（运维临时切换）→ yaml。
	listenAddr := envx.OrDefault("LIUSHA_API_ADDR", cfg.API.ListenAddr)

	// 沙盒工作区（文件 CRUD 面）：与 runner 的沙盒 bind mount 同一宿主
	// 目录——容器化部署经 compose 以同路径挂载，路径值两侧一致。
	workspaceRoot := cfg.Sandbox.WorkspaceHostDir
	if workspaceRoot == "" {
		def, err := sandbox.DefaultWorkspaceHostDir()
		if err != nil {
			logger.Fatal().Err(err).Msg("解析沙盒工作区默认目录失败")
		}
		workspaceRoot = def
	}
	workspaceStore, err := workspace.NewStore(workspaceRoot)
	if err != nil {
		logger.Fatal().Err(err).Str("root", workspaceRoot).Msg("打开沙盒工作区失败")
	}
	logger.Info().Str("root", workspaceRoot).Msg("沙盒工作区已就绪")

	// 附件域：复用工作区 Store 的路径钳制与落盘（暂存/落位都在同一棵工作区树内，
	// 下发搬家用 rename——同根不跨文件系统）。
	attachmentStore := attachment.NewStore(workspaceStore)
	dispatchService.Attachments = attachmentStore
	srv := &http.Server{
		Addr: listenAddr, Handler: httpapi.NewServer(httpapi.Deps{
			Auth:               authStore,  // 登录认证（/auth 系列 + RequireAuth 会话校验）
			AuthAudit:          auditStore, // 登录/登出/改密事件审计
			CookieSecure:       os.Getenv("LIUSHA_COOKIE_SECURE") == "true",
			Workspaces:         workspaceStore,
			Credentials:        credAPI,
			CredentialAudit:    auditStore, // 凭证库变更/明文读取审计（敏感操作面）
			TaskStatus:         dispatch.TaskStatusAdapter{Tasks: taskStore},
			Findings:           findStore, // 全局漏洞台账（人工下发 + 代理捕获全量 + triage 处置）
			Invocations:        invocationStore,
			Chat:               dispatchService, // POST /chat 会话发起扫描
			FollowUp:           dispatchService, // 多轮：POST /conversations/:id/messages 动作续接
			Abort:              dispatchService, // 多轮：POST /conversations/:id/abort 停止会话关联扫描
			Deleter:            dispatchService, // DELETE /conversations/:id 删会话+消息；关联扫描进行中拒删（409，先停后删）
			Attachments:        attachmentStore, // 附件上传（POST /attachments、POST /conversations/:id/attachments）
			AttachmentContext:  attachmentContext{conversationStore, taskStore},
			AttachmentMaxBytes: cfg.Conversation.AttachmentMaxBytes,
			Renamer:            conversationStore,                     // PATCH /conversations/:id 重命名标题（conversationStore.SetTitle 直接满足）
			ConfigStore:        cfgStore,                              // agent 配置 CRUD（配置管理页）
			SkillStore:         cfgStore,                              // skill 配置 CRUD（知识库管理页），走多级缓存
			ToolCatalog:        toolCatalog,                           // GET /tools、/tools/:name：工具目录检索/详情 + 智能体选工具
			Models:             llmStore,                              // GET/POST/PUT/DELETE /models：provider 部署 CRUD + 角色路由面板
			KeyEncrypter:       llmKeyCipher,                          // POST/PUT /models/providers：加密前端直填的明文 API Key
			ProviderTester:     providerTester,                        // POST /models/providers/test|list-models：实连探测（不落库）
			Settings:           settingStore,                          // GET/PUT /settings 系列：compaction/runtime/proxy_filter 三组业务旋钮
			Traffic:            proxyTrafficStore,                     // GET /traffic 系列：代理捕获流量只读浏览（流量模块）
			TrafficConv:        conversationStore,                     // 流量详情消费关系 chip → task 反解会话 id 跳转
			Conversations:      conversationStore,                     // 会话列表 / 消息回看
			EventStream:        dispatch.EventStreamAdapter{Rdb: rdb}, // SSE 订阅 redis 事件
			UsageTasks:         conversationStore,                     // 会话用量：会话→task 解析
			UsageLLM:           invocationStore,                       // 会话用量：LLM token/耗时合计
			UsageTools:         toolStore,                             // 会话用量：工具耗时合计
			ExplorationGraph:   explorationGraphAdapter,               // Phase 1: 探索图 API（e2e 测试迁移）
			Tasks:              taskStore,                             // GET /tasks 系列：任务模块列表/详情（派生列 JOIN）
			TaskDispatch:       dispatchService,                       // POST /tasks：任务模块「新建任务」批量/定时下发
			TaskControl:        dispatchService,                       // POST /tasks/:id/control：任务行 暂停/继续/停止
			Logs:               logReader,                             // GET /logs 系列：日志页（尾读 logs/{process}.log）
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

// ensureInitialAdmin 在账号表为空时用 env 密码建 admin（仅首启生效，之后永久
// 跳过；后续改密走前端）。凭据随进程启动即存在——不存在"服务已起、账号未建"
// 的公网抢占窗口，api 全程没有任何未认证的建号端点。口径与全仓 fail-fast 密钥一致。
func ensureInitialAdmin(ctx context.Context, accounts *auth.Store, logger zerolog.Logger) {
	n, err := accounts.CountAccounts(ctx)
	if err != nil {
		logger.Fatal().Err(err).Msg("读取账号数失败")
	}
	if n > 0 {
		return
	}
	pw := os.Getenv("LIUSHA_ADMIN_INITIAL_PASSWORD")
	if len(pw) < 8 || len(pw) > 72 {
		logger.Fatal().Msg("首次启动需要 LIUSHA_ADMIN_INITIAL_PASSWORD（8-72 字节）初始化 admin 账号；仅首启生效，之后改密走前端")
	}
	hash, err := auth.HashPassword(pw)
	if err != nil {
		logger.Fatal().Err(err).Msg("初始密码哈希失败")
	}
	if _, err := accounts.CreateAccountIfAbsent(ctx, "admin", hash); err != nil {
		logger.Fatal().Err(err).Msg("建立初始管理员失败")
	}
	logger.Info().Msg("初始管理员已建立（admin），建议登录后立即修改密码")
}

// retryOptionsFromConfig 把 yaml 配置（秒数列表 + 毫秒）翻译成框架退避表。
// 任一字段为 0 由框架构造器逐项兜底为 DefaultRetryOptions。
func retryOptionsFromConfig(c config.RetryConfig) fwllm.RetryOptions {
	return fwllm.RetryOptions{
		MaxRetries429: c.Max429,
		MaxRetries529: c.Max529,
		MaxRetries5xx: c.Max5xx,
		MaxRetriesNet: c.MaxNet,
		Backoff429:    config.AsDurations(c.Backoff429Seconds),
		Backoff5xx:    config.AsDurations(c.Backoff5xxSeconds),
		BackoffNet:    config.AsDurations(c.BackoffNetSeconds),
		Backoff529:    time.Duration(c.Backoff529Millisec) * time.Millisecond,
	}
}

// attachmentContext 组合 conversation/task 两 store 满足 httpapi.AttachmentContext
// （会话→任务→批次解析链，供附件落位定 inbox 目录）。
type attachmentContext struct {
	Conversations *conversation.Store
	Tasks         *task.Store
}

func (a attachmentContext) ResolveTaskID(ctx context.Context, conversationID string) (string, error) {
	return a.Conversations.ResolveTaskID(ctx, conversationID)
}

func (a attachmentContext) GetSummary(ctx context.Context, id string) (task.Summary, error) {
	return a.Tasks.GetSummary(ctx, id)
}

// llmReadiness 把 LLM Router 适配为下发边界的就绪校验：For(simple) 是全部档位的
// 降级兜底——simple 可解析 ⟺ 任意档位可解析，一次调用即完备校验（含密钥来源存在性）。
type llmReadiness struct{ r *fwllm.Router }

func (a llmReadiness) Ready(ctx context.Context) error {
	_, err := a.r.For(ctx, fwllm.ComplexitySimple)
	return err
}
