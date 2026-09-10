// Package main 是 liusha runner 进程入口。
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入 agent 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个 agent agent
//	  3. healthz HTTP；graceful shutdown
//
// **部署约束：runner 当前是单实例**。subtask swarm 用 in-process parentRegistries
// (sync.Map) 持有planner的 Registry + exploitation goroutine——planner一旦被 asynq 路由到本进程，
// 它派的所有exploitation也只在本进程内跑（共享 ctx 树 + sandbox 容器 + WaitAll 清理）。
// 多实例部署需先实现 Registry 跨进程协同（如 Redis-backed Registry）才能解锁。
// active planner在 enqueue 时已设 asynq.MaxRetry(0)，crash 后不重试——配合本约束
// 避免"planner 在 A 实例 crash → asynq retry 给 B → B 看不到 A 内存的 exploitation Registry"僵尸场景。
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	agentstore "github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/setting"
	cfgcache "github.com/V3teran/liusha/internal/cache"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/domain"
	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/eventbus"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/ingestor"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/ratelimit"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/skillstore"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/V3teran/liusha/internal/knowledgegraph"

	"github.com/hibiken/asynq"
)

const (
	// scanReaperInterval：reaper 巡逻周期。两条 (status, heartbeat_at) 索引 UPDATE，常态命中 0 行，
	// 30s 既灵敏又不扰库。
	scanReaperInterval = 30 * time.Second
	// scanReaperStaleBuffer：判死阈值在「最长合法工具+LLM 间隔」之上再加的安全缓冲（时钟偏移 / 调度抖动）。
	scanReaperStaleBuffer = 2 * time.Minute
)

func main() {
	logger := logx.New("runner")
	ctx := context.Background()

	cfg, err := config.Load(envx.OrDefault("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}
	runnerCfg := cfg.Runner

	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"),
		cfg.Postgres.MaxConns, cfg.Postgres.MinConns,
		cfg.Postgres.ConnectTimeoutSeconds, cfg.Postgres.MaxConnLifetimeSeconds)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	redisAddr := os.Getenv("LIUSHA_REDIS_ADDR")
	rdb, err := db.NewRedis(ctx, redisAddr, cfg.Redis)
	if err != nil {
		logger.Fatal().Err(err).Msg("redis")
	}
	defer rdb.Close()

	// Stores
	taskStore := task.NewStore(pool)               // 统一 task store（合并 active_scan + passive_session）
	assignmentStore := assignment.NewStore(pool)   // 聚合建 passive assignment（一切 task 皆属某 assignment）
	convStore := conversation.NewStore(pool)       // 会话/消息 store（阶段B 过程事件落库）
	eventPublisher := scanstream.NewPublisher(rdb) // 过程事件实时广播（阶段B redis 管道）
	executorRuns := agentstore.NewStore(pool)
	finds := finding.NewStore(pool)
	worldStore := knowledgegraph.NewStore(pool) // L3 世界模型持久层（onboard 落 KindObjective 节点）
	toolCalls := toolinvocation.NewStore(pool)
	calls := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	corpusStore := corpus.NewStore(pool) // 跨目标知识库（hybrid RAG）
	defer func() { _ = calls.Close() }()
	proxyStore := traffic.NewProxyStore(pool) // 代理捕获流量（passive，按 host）
	agentStore := traffic.NewAgentStore(pool) // agent 自产流量（active，按 task）
	creds := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	// 情报黑板：assignment 级别的长期情报共享，PostgreSQL 持久化，按 assignment_id 隔离
	leads := insight.NewStore(pool)

	// Jina embedding + rerank client（corpus hybrid RAG 用）。密钥走 ENV JINA_API_KEY；
	// 缺失时 jinaClient=nil，corpus 降级（search 退纯 sparse、write 不 embed）——不阻塞渗透主流程。
	var embedder corpus.Embedder
	var reranker corpus.Reranker
	if jc, err := embedding.NewClient(os.Getenv("JINA_API_KEY")); err != nil {
		logger.Warn().Err(err).Msg("JINA_API_KEY 未配置，corpus 降级为纯 sparse 检索（不影响主流程）")
	} else {
		embedder = jc
		reranker = jc
	}

	// executor system prompt 已编译期 embed（internal/builder/executor/system_prompt.md），
	// 不再需要运行时 skill loader 加载——下面的 vuln/tooling loader 服务 Progressive Disclosure。

	// Tooling loader（Progressive Disclosure）：root=skills/tooling，
	// 每个子目录一份 SKILL.md = 一个外部 CLI 工具的完整手册。
	// agent buildUserPrompt 用 List() 拼"工具索引"段（Tier 1）；
	// LLM 调 read_tooling_skill(name) 拿完整 body（Tier 2）。
	// 目录不存在或扫描失败 → 置 nil，agent 自动 fallback 不注入索引段、不注册工具。
	toolingLoader := skill.NewLoader(filepath.Join(cfg.Skills.Root, "tooling"))
	if _, err := toolingLoader.Index(); err != nil {
		logger.Warn().Err(err).Str("root", filepath.Join(cfg.Skills.Root, "tooling")).
			Msg("tooling skill index 失败（read_tooling_skill 与工具索引段将不可用）")
		toolingLoader = nil
	} else {
		toolingNames := make([]string, 0)
		for _, c := range toolingLoader.List() {
			toolingNames = append(toolingNames, c.Name)
		}
		logger.Info().Strs("tooling_skills", toolingNames).Msg("tooling skill index loaded")
	}

	// Tools manifest（tools.yaml）：与 Dockerfile 装的 binary 严格对应——
	// agent 用它渲染 SystemPrompt 的 tooling_catalog 段（Tier 1 索引）。
	// 与 SKILL.md frontmatter 解耦：删 SKILL ≠ 工具消失。
	// 路径可通过 LIUSHA_TOOLS_MANIFEST_PATH env override，缺省 deployments/tool-images/pentools/tools.yaml。
	toolsManifestPath := envx.OrDefault("LIUSHA_TOOLS_MANIFEST_PATH", "deployments/tool-images/pentools/tools.yaml")
	toolsManifest, err := manifest.Load(toolsManifestPath)
	if err != nil {
		logger.Fatal().Err(err).Str("path", toolsManifestPath).Msg("tools.yaml 加载失败——LLM 看不到沙箱工具会无法 ReAct，fail-fast")
	}
	logger.Info().Strs("tools", toolsManifest.Names()).Int("count", len(toolsManifest.Tools)).Str("path", toolsManifestPath).Msg("tools manifest loaded")

	// L2 域适配注册表：注册各域 Profile（目标接入/工具镜像/finding schema）。
	// 加新域 = New 一个 Profile 并 Register，此处外无核心改动（架构试金石）。
	profiles := domain.NewRegistry()
	profiles.Register(executor.New())
	logger.Info().Strs("domains", profiles.Domains()).Msg("domain profiles registered")

	// Vuln loader（Progressive Disclosure）：root=skills/vuln，
	// 每个子目录一份 SKILL.md = 一种漏洞类型的挖掘指南。
	// agent buildUserPrompt 用 List() 拼"漏洞挖掘指南索引"段（Tier 1）；
	// LLM 按 user_prompt 注入的"漏洞类型索引"判完方向后调 read_vuln_skill(name) 拿完整 body（Tier 2）。
	// 目录不存在或扫描失败 → 置 nil，agent 自动 fallback 不注入索引段、不注册工具。
	vulnLoader := skill.NewLoader(filepath.Join(cfg.Skills.Root, "vuln"))
	if _, err := vulnLoader.Index(); err != nil {
		logger.Warn().Err(err).Str("root", filepath.Join(cfg.Skills.Root, "vuln")).
			Msg("vuln skill index 失败（read_vuln_skill 与漏洞挖掘指南索引段将不可用）")
		vulnLoader = nil
	} else {
		vulnNames := make([]string, 0)
		for _, c := range vulnLoader.List() {
			vulnNames = append(vulnNames, c.Name)
		}
		logger.Info().Strs("vuln_skills", vulnNames).Msg("vuln skill index loaded")
	}

	// Asynq Client
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	defer wc.Close()

	// LLM Router：yaml retry 配置接线（兜底 spec §8.5 退避表）
	// 容器化沙箱启动器（管理 sandbox 容器生命周期：per agent run 一个容器）。
	// 启动时一次性清理上次进程崩前残留的孤儿容器——max lifetime 4h + Destroy 失败兜底。
	//
	// active 容器内抓流量 → agent_traffic（source=internal）：
	//   - 浏览器：browser-svc.py 内建 CDP Network observer 抓 chromium 真实流量
	//   - CLI：容器内本地 mitmproxy + mitm-capture.py，工具经 HTTP_PROXY 走它
	// 两者都经 LIUSHA_INGEST_URL POST 到 runner 自己的 ingest endpoint（见下方 hsMux 注册）。
	// 凭证共享走 redis credentials key（read/write_credential）。
	launcher := sandbox.NewDockerLauncher(cfg.Sandbox.DefaultImage)
	// 注入视口尺寸到 launcher → docker run -e → 容器内 wrapper 透传 chromium。
	launcher.ViewportWidth = cfg.Sandbox.ViewportWidth
	launcher.ViewportHeight = cfg.Sandbox.ViewportHeight
	// 拼 ingest URL/token 注入 launcher → docker run -e。runner 跑在 host，容器经
	// host.docker.internal 回连 runner 自己的 healthz 端口（cfg.Runner.HealthzAddr，默认 :9090）。
	// token 与本进程 ingest handler 共享同一值（ENV LIUSHA_INGEST_TOKEN 覆盖 yaml）。
	// 解析失败则不注入 → 沙箱读不到 LIUSHA_INGEST_URL → capture 不启用。
	// 解析失败则不注入 → browser-svc.py/mitm-capture.py 读不到 LIUSHA_INGEST_URL，capture 不启用。
	if _, port, splitErr := net.SplitHostPort(runnerCfg.HealthzAddr); splitErr != nil {
		logger.Warn().Err(splitErr).Str("healthz_addr", runnerCfg.HealthzAddr).
			Msg("解析 runner healthz addr 失败，跳过 ingest 注入（capture 不启用）")
	} else {
		launcher.IngestURL = "http://host.docker.internal:" + port + "/internal/v1/flows/ingest"
		launcher.IngestToken = envx.OrDefault("LIUSHA_INGEST_TOKEN", cfg.Proxy.IngestToken)
	}
	if err := launcher.CleanupOrphans(ctx); err != nil {
		logger.Warn().Err(err).Msg("CleanupOrphans 失败（非致命，max lifetime 兜底）")
	}

	// executorDeps 已拆平到 handler 各字段，无需独立 Deps 结构体。

	// 共享多级缓存内核（L1 内存 + L2 redis + 跨进程失效总线）：一条 Subscribe 循环
	// 覆盖全部配置资源。Subscribe 阻塞运行（内部 for-select 直到 ctx 取消），必须后台起——
	// 同步调用会把 main goroutine 卡死在订阅循环，后续 reaper / healthz 永不启动。
	cache := cachestore.New(rdb, 0)
	go func() {
		if err := cache.Subscribe(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("cachestore 失效订阅退出——配置跨进程失效不可用")
		}
	}()

	// 配置事实源（DB + 内存/redis 缓存）：运行期按需读 agent 装配引擎。
	// 文件仅是首次导入的种子（seed 导入在别处），进程运行期一律走 DB/缓存（见 D6/D7）。
	cfgStore := cfgcache.New(pool, cache)

	// LLM 配置事实源：provider.Router 运行期按 tier 解析 provider 部署即读它（多级缓存）。
	// api 进程改「LLM 配置」模块后经 cachestore 广播失效，runner 下次 For(tier) 即读到最新部署。
	// agent.tier 覆盖 agent→tier 第一跳（0107）：agent 在「智能体」页改档后，api 经 cachestore
	// 广播失效 tier 键，runner 被动清 L1，下次 For(tier) 即读到新档（TierByCode 走多级缓存）。
	llmStore := llmstore.New(pool, cache).
		WithComplexityOverride(llmstore.AgentComplexityOverride(cfgStore, logger))

	// LLM provider API Key 加密密钥（migration 0103）：同 cmd/api 的 fail-fast 校验——
	// runner 是解密密钥、真正拿明文打 LLM 请求的一端，缺密钥直接拒启动。
	llmKeyCipher, err := cryptx.NewFromEnv("LIUSHA_LLM_KEY_SECRET")
	if err != nil {
		logger.Fatal().Err(err).Msg("LIUSHA_LLM_KEY_SECRET 未配置或不合法——provider 密钥解密需要它（fail-fast）")
	}

	// 业务旋钮事实源（react/runtime/proxy_filter 分组 KV）：handler 运行期现读 react/runtime，
	// api 进程改「系统配置」后经 cachestore 广播失效，runner 下次读即拿到最新旋钮（真热改）。
	settingStore := settingstore.New(pool, cache)

	// handler
	// per-host 并发信号量（§4.3）。TTL = agent 超时 + 10min 缓冲：防长 task 运行期计数键被
	// TTL 误清导致 host 额度漂移；持有者崩溃时靠 TTL 到期兜底清零，不永久泄漏。
	hostSemTTL := time.Duration(runnerCfg.AgentRunTimeoutSeconds)*time.Second + 10*time.Minute
	hostSem := ratelimit.NewHostSemaphore(rdb, cfg.Credential.RedisKeyPrefix, runnerCfg.PerHostConcurrency, hostSemTTL)

	// Sandbox Manager：按 Assignment 粒度管理容器，多 Task 共享，带引用计数
	sandboxMgr := sandbox.NewPooledManager(launcher, logger, 30000) // 30 秒 grace period

	// EventBus：事件驱动的 Planner Agent 基础设施（进程单例，跨 Task 共享）
	eventBusCtx, eventBusCancel := context.WithCancel(context.Background())
	defer eventBusCancel()
	eventBus := executor.NewPlannerEventBus(eventBusCtx)

	// PlanStore：execution_plan 表的持久化层
	// knowledgegraph.Store 在前面已初始化为 worldStore

	// ControlPlane：task_control_event 表的持久化层（人工干预）
	controlPlaneStore := controlplane.NewStore(pool)

	// PlannerAgentManager：管理所有 Planner Agent 的生命周期
	plannerMgr := newPlannerAgentManager(logger)
	defer plannerMgr.StopAll()

	// Action 级别事件总线（Executor 监听 Planner 的 Kill/Steer 事件）
	actionBus := eventbus.New()

	h := handler{
		executors:      executorRuns,
		tasks:          taskStore,
		findings:       finds,
		corpus:         corpusStore,
		embedder:       embedder,
		reranker:       reranker,
		leads:          leads,
		proxyStore:     proxyStore,
		agentStore:     agentStore,
		calls:          calls,
		hostSem:        hostSem,
		settings:       settingStore,
		runnerCfg:      runnerCfg,
		sandboxMgr:     sandboxMgr,
		logger:         logger,
		router:         provider.NewRouter(llmStore.AsRouterStore(), llmKeyCipher),
		creds:          creds,
		toolCalls:      toolCalls,
		toolingLoader:  toolingLoader,
		vulnLoader:     vulnLoader,
		toolsManifest:  toolsManifest,
		cfgStore:       cfgStore,
		conversations:  convStore,
		eventPublisher: eventPublisher,
		profiles:       profiles,
		world:          worldStore,
		checkpoint:     executor.NewPGCheckpointStore(pool),
		eventBus:       eventBus,
		actionBus:      actionBus,
		plannerMgr:     plannerMgr,
		controlPlane:   controlPlaneStore,
	}

	mux := worker.NewMux()
	mux.Register(worker.RoleExecutor, h.handle)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: runnerCfg.AsynqConcurrency,
			Queues: map[string]int{
				worker.QueueExecutor: runnerCfg.QueueAgentWeight,
				worker.QueueDispatch: runnerCfg.QueueDispatchWeight,
			},
		},
	)

	// Ingestor goroutine：消费 Redis Stream → 启发式 → 入 main 队列。
	trafficCtx, trafficCancel := context.WithCancel(context.Background())
	defer trafficCancel()

	trafficIngestor, err := ingestor.NewTraffic(trafficCtx, ingestor.Deps{
		Redis:         rdb,
		Cfg:           cfg.Ingestor,
		Stream:        cfg.Proxy.StreamName,
		Tenant:        cfg.Credential.RedisKeyPrefix,
		Assignments:   assignmentStore,
		Tasks:         taskStore,
		ProxyStore:    proxyStore,
		AgentStore:    agentStore,
		Agents:        executorRuns,
		Conversations: convStore, // passive 聚合建 task 后建会话流
		Enqueuer:      wc,
		Logger:        logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("new ingestor.traffic")
	}
	go func() {
		if err := trafficIngestor.Run(trafficCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("ingestor.traffic exited")
		}
	}()

	// task reaper goroutine（B2 进度探活）：task.heartbeat_at 由 agent 每次工具调用驱动续命
	// （见 toolRecordInterceptor 心跳逻辑）+ handler 入口重置一次。runner 进程崩溃或扫描卡死后心跳停摆，
	// reaper 据此把超时孤儿判为 aborted——否则前端永远显示「进行中」。
	//
	// task 是有界运行，跑完即终态，无常驻监控会话概念，无 TTL 轮换。reaper 统一判活：
	// swarm run 内可能跑长工具（sqlmap/nmap）+ 慢 LLM，staleAfter 较长；solo 单批分析轻量，
	// 用同一 staleAfter 亦安全（偏保守不冤杀）。
	//
	// staleAfter 必须 > 单 run 内两次工具调用之间的最长合法间隔，否则冤杀正在干活的扫描：
	// 最长间隔 ≈ 一次长工具执行(step_tool_timeout) + 决定下一步的 LLM 生成(step_llm_timeout) +
	// 可能的上下文压缩 LLM(step_llm_timeout) + 缓冲。据此动态推导，不写死。
	go func() {
		staleAfter := time.Duration(cfg.Toolruntime.StepToolTimeoutSeconds+2*cfg.Runner.StepLLMTimeoutSeconds)*time.Second + scanReaperStaleBuffer
		logger.Info().Dur("stale_after", staleAfter).Dur("interval", scanReaperInterval).Msg("task reaper started")
		ticker := time.NewTicker(scanReaperInterval)
		defer ticker.Stop()
		for {
			select {
			case <-trafficCtx.Done():
				return
			case <-ticker.C:
				if n, err := taskStore.ReapStale(trafficCtx, staleAfter); err != nil {
					logger.Warn().Err(err).Msg("task reap stale failed")
				} else if n > 0 {
					logger.Warn().Int("aborted", n).Dur("stale_after", staleAfter).
						Msg("task 心跳超时回收（进程崩溃或扫描卡死）")
				}
			}
		}
	}()

	// healthz HTTP + active 抓流量 ingest 端点（从 cmd/proxy 迁来）。
	// 沙箱内 CLI(本地 mitmproxy)/浏览器(CDP) 抓的流量 POST 到这里 → trafficIngestor.SubmitInternal
	// 直接入进程内队列 → drain goroutine 落 agent_traffic。同进程直送，不再绕 redis（internal 自环冗余）。
	hsMux := http.NewServeMux()
	hsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	hsMux.HandleFunc("/internal/v1/flows/ingest",
		newIngestHandler(trafficIngestor, envx.OrDefault("LIUSHA_INGEST_TOKEN", cfg.Proxy.IngestToken), logger))
	hs := &http.Server{
		Addr:              runnerCfg.HealthzAddr,
		Handler:           hsMux,
		ReadHeaderTimeout: time.Duration(cfg.API.ReadHeaderTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info().Str("addr", hs.Addr).Msg("runner healthz listening")
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("healthz serve")
		}
	}()

	go func() {
		logger.Info().Msg("asynq server starting")
		if err := srv.Run(mux.AsynqMux()); err != nil {
			logger.Fatal().Err(err).Msg("asynq run")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	logger.Info().Str("signal", sig.String()).Msg("runner shutting down")

	// 清理所有活跃 Sandbox 容器
	if err := sandboxMgr.DestroyAll(context.Background()); err != nil {
		logger.Warn().Err(err).Msg("sandboxMgr.DestroyAll 失败（best-effort）")
	}

	trafficCancel()
	asynqDone := make(chan struct{})
	go func() {
		srv.Shutdown()
		close(asynqDone)
	}()
	select {
	case <-asynqDone:
		logger.Info().Msg("asynq shutdown clean")
	case <-time.After(time.Duration(runnerCfg.AsynqShutdownTimeoutSeconds) * time.Second):
		logger.Warn().Int("timeout_seconds", runnerCfg.AsynqShutdownTimeoutSeconds).
			Msg("asynq shutdown timeout — in-flight tasks may be aborted")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(runnerCfg.ShutdownTimeoutSeconds)*time.Second)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("healthz shutdown")
	}
	logger.Info().Msg("runner stopped")
}

// handler struct + failTask/abortTask/handle 入口 已抽到 handler.go。
// handlePassive 在 handler_passive.go；handleActive 在 handler_active.go。
//
// brief → host 抽取已迁至 L2 域注册表（handler.onboardHost → executor.Registry.Onboard）：
// host 抽取归各域 Profile，核心不再持有 briefHostRe 正则。
