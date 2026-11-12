// Package runner 是 runner 组件：消费任务队列跑四 agent 认知循环（工具经 sandbox.url 在沙盒
// 执行）、摄入代理流量、接收沙盒浏览器回灌的流量，并运行恢复监控与各类清扫器。
//
// 部署约束：runner 是单实例。认知循环的进程内状态（事件总线、浏览器会话）只在本进程内；
// 任务入队时已设 asynq.MaxRetry(0)，崩溃后不重投到别的实例，由恢复监控在本实例续跑。
package runner

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/distill"
	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/ingestor"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/runner"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/settingstore"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
)

// stores 聚合全部持久层句柄。
type stores struct {
	tasks         *task.Store
	assignments   *assignment.Store
	conversations *conversation.Store
	agentRuns     *agentrun.Store
	findings      *finding.Store
	graph         *explorationgraph.Store
	toolCalls     *toolinvocation.Store
	proxyStore    *traffic.ProxyStore
	agentStore    *traffic.AgentStore
	corpus        *corpus.Store
	calls         *llminvocation.Store
}

func newStores(pool *pgxpool.Pool, cfg config.AppConfig) *stores {
	return &stores{
		tasks:         task.NewStore(pool),
		assignments:   assignment.NewStore(pool),
		conversations: conversation.NewStore(pool),
		agentRuns:     agentrun.NewStore(pool),
		findings:      finding.NewStore(pool),
		graph:         explorationgraph.NewStore(pool),
		toolCalls:     toolinvocation.NewStore(pool),
		proxyStore:    traffic.NewProxyStore(pool), // 代理捕获流量（按 host）
		agentStore:    traffic.NewAgentStore(pool), // agent 自产流量（按 task）
		corpus:        corpus.NewStore(pool),       // 跨目标知识库（hybrid RAG）
		calls:         llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation),
	}
}

// Run 运行 runner 组件直到 ctx 取消。启动失败返回错误。
func Run(ctx context.Context, cfg config.AppConfig) error {
	logger := logx.New("runner")
	runnerCfg := cfg.Runner

	pool, err := db.NewPgPool(ctx, cfg.Postgres, logger)
	if err != nil {
		return fmt.Errorf("连接 PostgreSQL: %w", err)
	}
	defer pool.Close()

	rdb, err := db.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("连接 Redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	if cfg.Sandbox.Token == "" {
		return errors.New("sandbox.token 未配置（环境变量 LIUSHA_SANDBOX_TOKEN）：runner 调用沙盒与接收其流量回灌都需要它")
	}
	// runner 是解密密钥、真正拿明文打 LLM 请求的一端，缺密钥直接拒启动。
	keyCipher, err := cryptx.NewFromEnv("LIUSHA_LLM_KEY_SECRET")
	if err != nil {
		return fmt.Errorf("LIUSHA_LLM_KEY_SECRET 未配置或不合法（provider 密钥解密需要它）: %w", err)
	}

	st := newStores(pool, cfg)
	defer func() { _ = st.calls.Close() }()

	// 上一实例停机时仍在 running 的 action 标记为 failed（其执行者已不存在）。
	if recovered, err := st.graph.RecoverOrphanedActions(ctx); err != nil {
		logger.Error().Err(err).Msg("恢复孤儿 action 失败")
	} else if recovered > 0 {
		logger.Info().Int("count", recovered).Msg("已恢复上一实例遗留的孤儿 action")
	}

	embedder, reranker := newCorpusEmbedder(logger)
	tools := manifest.Default()

	redisOpt := asynq.RedisClientOpt{Addr: cfg.Redis.Addr, Password: cfg.Redis.Password}
	wc := worker.NewClient(redisOpt)
	defer func() { _ = wc.Close() }()

	// 共享多级缓存内核（L1 内存 + L2 redis + 跨进程失效总线）：一条 Subscribe 循环覆盖全部配置资源。
	cache := cachestore.New(rdb, 0)
	go func() {
		if err := cache.Subscribe(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("cachestore 失效订阅退出——配置跨进程失效不可用")
		}
	}()

	// agent 与技能配置：出厂值编译在程序里，用户层经三级缓存读取；api 改配置后广播失效，下个任务即用新配置。
	cfgStore := configstore.New(pool, cache, configstore.ToolCatalogFrom(tools))
	llmStore := llmstore.New(pool, cache)
	sysSettings := settingstore.New(pool, cache, settingstore.DefaultsFrom(cfg))

	router := llm.NewRouterWithOpts(llmStore.AsRouterStore(keyCipher), cfg.LLM.Retry.Options())
	// LLM 调用装配：单步看门狗（挂死的调用会阻塞 agent 事件循环）+ llm_invocation 审计（含失败）。
	// 看门狗每次调用现读热配置 runtime.step_llm_timeout_seconds，读失败回落部署值。
	stepLLMTimeout := time.Duration(runnerCfg.StepLLMTimeoutSeconds) * time.Second
	router.SetProviderWrapper(func(p llm.Provider) llm.Provider {
		return llm.InstrumentProvider(llm.WithDynamicCallTimeout(p,
			stepLLMTimeout,
			func(ctx context.Context) time.Duration {
				rt, err := sysSettings.Runtime(ctx)
				if err != nil {
					return 0
				}
				return time.Duration(rt.StepLLMTimeoutSeconds) * time.Second
			}), llminvocation.UsageSink{Store: st.calls})
	})

	creds := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	h := &runner.Handler{
		AgentRuns:       st.agentRuns,
		Tasks:           st.tasks,
		Findings:        st.findings,
		Sandbox:         sandbox.NewClient(cfg.Sandbox.URL, cfg.Sandbox.Token),
		SandboxListener: resolveSandboxContract(cfg, logger),
		Corpus:          st.corpus,
		Embedder:        embedder,
		Reranker:        reranker,
		ProxyStore:      st.proxyStore,
		AgentStore:      st.agentStore,
		Calls:           st.calls,
		Settings:        sysSettings,
		RunnerCfg:       runnerCfg,
		Logger:          logger,
		Router:          router,
		Creds:           creds,
		Audit:           audit.NewStore(pool),
		ToolCalls:       st.toolCalls,
		Skills:          skill.NewStoreReader(cfgStore),
		ToolsManifest:   tools,
		CfgStore:        cfgStore,
		Conversations:   st.conversations,
		EventPublisher:  convstream.NewPublisher(rdb),
		Graph:           st.graph,
		EventBus:        bus.New(ctx),
		ControlPlane:    controlplane.NewStore(pool),
		// 任务终态经验蒸馏：embedder 缺席时教训降级纯 sparse 检索，不阻塞写入。
		Distiller: distill.New(router, st.graph, st.corpus, embedder, logger),
	}

	mux := worker.NewMux()
	mux.Register(worker.RoleAgent, h.Handle)

	// 后台循环共用一个 ctx：停机第一步取消它，停止接收新流量与各类巡检。
	bgCtx, stopBackground := context.WithCancel(ctx)
	defer stopBackground()

	trafficIngestor, err := ingestor.NewTraffic(bgCtx, ingestor.Deps{
		Redis:         rdb,
		Cfg:           cfg.Ingestor,
		Stream:        cfg.Proxy.StreamName,
		Assignments:   st.assignments,
		Tasks:         st.tasks,
		ProxyStore:    st.proxyStore,
		AgentStore:    st.agentStore,
		AgentRuns:     st.agentRuns,
		Conversations: st.conversations,
		Enqueuer:      wc,
		Logger:        logger,
	})
	if err != nil {
		return fmt.Errorf("构造流量摄入器: %w", err)
	}
	go func() {
		if err := trafficIngestor.Run(bgCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("ingestor.traffic exited")
		}
	}()

	startBackgroundLoops(bgCtx, cfg, pool, wc, st, sysSettings, creds, rdb, logger)

	srv := asynq.NewServer(redisOpt, asynq.Config{
		Concurrency: runnerCfg.AsynqConcurrency,
		Queues:      map[string]int{worker.QueueAgent: runnerCfg.QueueAgentWeight},
		// asynq 内部日志（投递重试 / 处理错误）事关任务是否丢失，接进 logx。
		Logger: asynqLogAdapter{logger},
	})
	if err := srv.Start(mux.AsynqMux()); err != nil {
		return fmt.Errorf("启动任务消费者: %w", err)
	}

	hs := newHealthGateway(cfg, trafficIngestor, logger)
	gatewayErr := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", hs.Addr).Msg("runner healthz listening")
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			gatewayErr <- fmt.Errorf("runner 监听 %s: %w", hs.Addr, err)
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-gatewayErr:
	}
	shutdown(logger, runnerCfg, srv, hs, stopBackground)
	return runErr
}

// startBackgroundLoops 启动恢复监控与各类清扫器（随 ctx 取消退出）。
func startBackgroundLoops(ctx context.Context, cfg config.AppConfig, pool *pgxpool.Pool, wc *worker.Client,
	st *stores, sysSettings *settingstore.Store, creds *credential.RedisProvider, rdb *redis.Client, logger zerolog.Logger,
) {
	runnerCfg := cfg.Runner
	// 僵尸 run 回收与搁浅任务重投。
	runner.StartRunRecoveryMonitor(ctx, pool, wc, st.agentRuns, st.tasks, logger,
		time.Duration(runnerCfg.RunRecoveryIntervalSeconds)*time.Second,
		time.Duration(runnerCfg.RunRecoveryTimeoutSeconds)*time.Second,
		runnerCfg.RunRecoveryMaxAttempts)

	task.StartReaper(ctx, st.tasks, sysSettings, runnerCfg.StepLLMTimeoutSeconds, st.conversations, convstream.NewPublisher(rdb), logger)

	// 会话身份终态清扫：任务终态回收（handler 内）的崩溃兜底，按任务状态回收、不依赖过期时间。
	credential.StartSweepLoop(ctx, creds, func(ctx context.Context, taskID string) (string, bool, error) {
		t, err := st.tasks.GetByID(ctx, taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", false, nil
		}
		if err != nil {
			return "", false, err
		}
		return string(t.Status), true, nil
	}, logger)

	// llm_invocation 留存清理（retention_days > 0 时每日删除到期行）。
	llminvocation.StartRetentionSweep(ctx, st.calls, cfg.LLM.Invocation, logger)

	// action 恢复监控：判定阈值须超过单步 LLM 看门狗（一次长 LLM 调用是合法活动形态）再加缓冲。
	// 启动时读一次热配置，进程内不热变——启发式缓冲对慢漂移不敏感。
	recoveryStepLLM := time.Duration(runnerCfg.StepLLMTimeoutSeconds) * time.Second
	if rt, err := sysSettings.Runtime(ctx); err == nil && rt.StepLLMTimeoutSeconds > 0 {
		recoveryStepLLM = time.Duration(rt.StepLLMTimeoutSeconds) * time.Second
	}
	monitor := explorationgraph.NewRecoveryMonitor(st.graph, logger, 30*time.Second, recoveryStepLLM+time.Minute)
	go func() {
		if err := monitor.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("action recovery monitor stopped")
		}
	}()
}

// newCorpusEmbedder 构造知识库 embedding + rerank client。JINA_API_KEY 缺失时返回 nil，
// 知识库降级（检索退纯 sparse、写入不 embed），不阻塞主流程。
func newCorpusEmbedder(logger zerolog.Logger) (corpus.Embedder, corpus.Reranker) {
	jc, err := embedding.NewClient(os.Getenv("JINA_API_KEY"))
	if err != nil {
		logger.Warn().Err(err).Msg("JINA_API_KEY 未配置，corpus 降级为纯 sparse 检索（不影响主流程）")
		return nil, nil
	}
	return jc, jc
}

// resolveSandboxContract 解析沙盒对外可达性契约（监听段 + 回连地址），注入认知循环的工具契约。
// 段格式非法不致命：告警后按未提供处理（沙盒仍可用，仅无外部回连）。
func resolveSandboxContract(cfg config.AppConfig, logger zerolog.Logger) cognition.SandboxListener {
	start, count, err := sandbox.ParseListenerPorts(cfg.Sandbox.ListenerPorts)
	if err != nil {
		logger.Warn().Err(err).Str("listener_ports", cfg.Sandbox.ListenerPorts).
			Msg("监听段配置非法，沙盒对外监听不提供（反弹 shell / 代理不可达）")
	}
	host := cfg.Sandbox.CallbackHost
	if host == "" {
		host = detectCallbackHost()
		switch {
		case host == "":
			logger.Warn().Msg("回连地址自动探测失败（无默认路由？），建议显式配置 sandbox.callback_host")
		case runningInContainer():
			// 容器内探测到的是容器网内地址，外部目标不可达——不能静默注入一个看似可信的错地址。
			logger.Warn().Str("detected", host).
				Msg("runner 在容器内：自动探测的回连地址是容器网内 IP，外部目标不可达——请显式配置 sandbox.callback_host（宿主公网 IP / 域名）")
		}
	}
	logger.Info().
		Str("listener_ports", cfg.Sandbox.ListenerPorts).
		Int("port_start", start).
		Int("port_count", count).
		Str("callback_host", host).
		Msg("沙盒对外可达性契约已解析")
	return cognition.SandboxListener{PortStart: start, PortCount: count, CallbackHost: host}
}

// detectCallbackHost 取默认路由的源 IP 作为回连地址默认值。UDP Dial 不发包，仅让内核选路。
// NAT / 多网卡 / 域名 / 容器部署的探测值不正确，须显式配置 callback_host。
func detectCallbackHost() string {
	conn, err := net.Dial("udp", "192.0.2.1:80") // TEST-NET-1：仅选路，不可达也无妨
	if err != nil {
		return ""
	}
	defer func() { _ = conn.Close() }()
	if udp, ok := conn.LocalAddr().(*net.UDPAddr); ok {
		return udp.IP.String()
	}
	return ""
}

func runningInContainer() bool {
	_, err := os.Stat("/.dockerenv")
	return err == nil
}

// newHealthGateway 构造 healthz + 沙盒流量回灌端点：沙盒浏览器（CDP）抓的流量 POST 到这里，
// 直接入摄入器的进程内队列落 agent_traffic。
func newHealthGateway(cfg config.AppConfig, trafficIngestor *ingestor.Traffic, logger zerolog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/internal/v1/traffic/ingest", runner.NewIngestHandler(trafficIngestor, cfg.Sandbox.Token, logger))
	return &http.Server{
		Addr:              cfg.Runner.HealthzAddr,
		Handler:           mux,
		ReadHeaderTimeout: time.Duration(cfg.API.ReadHeaderTimeoutSeconds) * time.Second,
	}
}

// shutdown 优雅关停：停止流量入口与后台循环 → 排空任务（宽限期）→ healthz。
//
// 宽限期满后 asynq 把消息推回队列并取消 handler ctx，handler 按中断收尾（任务保持进行中，
// 释放其沙盒资源）。沙盒是独立组件，不随 runner 停止；同进程运行（serve）时由编排保证
// 沙盒在 runner 排空后才停，在途工具不会以「沙盒不可用」失败写进探索图。
func shutdown(logger zerolog.Logger, runnerCfg config.RunnerConfig, srv *asynq.Server, hs *http.Server, stopBackground context.CancelFunc) {
	stopBackground()
	drained := make(chan struct{})
	go func() {
		srv.Shutdown()
		close(drained)
	}()
	select {
	case <-drained:
		logger.Info().Msg("asynq shutdown clean")
	case <-time.After(time.Duration(runnerCfg.AsynqShutdownTimeoutSeconds) * time.Second):
		logger.Warn().Int("timeout_seconds", runnerCfg.AsynqShutdownTimeoutSeconds).
			Msg("asynq 停机超时：在途任务按中断收尾，由下次启动的恢复监控续跑")
	}

	hsCtx, hsCancel := context.WithTimeout(context.Background(), time.Duration(runnerCfg.ShutdownTimeoutSeconds)*time.Second)
	defer hsCancel()
	if err := hs.Shutdown(hsCtx); err != nil {
		logger.Error().Err(err).Msg("healthz shutdown")
	}
	logger.Info().Msg("runner stopped")
}

// asynqLogAdapter 把 asynq 内部日志桥接到 logx（asynq.Logger 接口适配）。
type asynqLogAdapter struct{ log zerolog.Logger }

func (a asynqLogAdapter) Debug(args ...any) { a.log.Debug().Msg(fmt.Sprint(args...)) }
func (a asynqLogAdapter) Info(args ...any)  { a.log.Info().Msg(fmt.Sprint(args...)) }
func (a asynqLogAdapter) Warn(args ...any)  { a.log.Warn().Msg(fmt.Sprint(args...)) }
func (a asynqLogAdapter) Error(args ...any) { a.log.Error().Msg(fmt.Sprint(args...)) }
func (a asynqLogAdapter) Fatal(args ...any) { a.log.Fatal().Msg(fmt.Sprint(args...)) }
