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
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/distill"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/ingestor"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/retrievalstatus"
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

func newStores(pool *pgxpool.Pool) *stores {
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
		calls:         llminvocation.NewStore(pool),
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

	redisOpts, err := db.RedisOptions(cfg.Redis)
	if err != nil {
		return err
	}
	rdb, err := db.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("连接 Redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	if cfg.Sandbox.Token == "" {
		return errors.New("sandbox.token 未配置（环境变量 LIUSHA_SANDBOX_TOKEN）：runner 调用沙盒与接收其流量回灌都需要它")
	}
	// runner 是解密密钥、真正拿明文打 LLM 请求的一端，缺密钥直接拒启动。
	keyCipher, err := cryptx.NewFromHex(cfg.LLM.EncryptionKey)
	if err != nil {
		return fmt.Errorf("llm.encryption_key（LIUSHA_LLM_ENCRYPTION_KEY）未配置或不合法: %w", err)
	}

	st := newStores(pool)
	defer func() { _ = st.calls.Close() }()

	tools := manifest.Default()

	redisOpt := worker.RedisOpt(redisOpts)
	wc := worker.NewClient(redisOpt)
	defer func() { _ = wc.Close() }()

	// 共享多级缓存内核（L1 内存 + L2 redis + 跨进程失效总线）：一条 Subscribe 循环覆盖全部配置资源。
	schemaVersion, err := db.SchemaVersion(ctx, pool)
	if err != nil {
		return err
	}
	cache := cachestore.New(rdb, schemaVersion)
	go func() {
		if err := cache.Subscribe(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("cachestore 失效订阅退出——配置跨进程失效不可用")
		}
	}()

	// agent、技能与场景配置经三级缓存读取；api 改配置后广播失效，下个任务即用新配置。
	cfgStore := configstore.New(pool, cache, configstore.ToolCatalogFrom(tools))
	llmStore := llmstore.New(pool, cache)
	sysSettings := settingstore.New(pool, cache, settingstore.DefaultsFrom(cfg))

	// 知识库：检索模型密钥运行期现取（模型配置页热改、失效广播即时生效），未配置退字面检索。
	kb := newCorpus(st.corpus, llmStore, keyCipher, rdb, logger)

	router := llm.NewRouterWithOpts(llmStore.AsRouterStore(keyCipher), cfg.LLM.Retry.Options(), cfg.LLM.AccountConcurrency)
	// 并发相关的生效值启动时落一行日志：部署方据此确认配置生效，排障时也不必回翻配置文件。
	logger.Info().
		Int("llm.account_concurrency", cfg.LLM.AccountConcurrency).
		Int("runner.concurrency", runnerCfg.Concurrency).
		Int("runner.action_parallelism", runnerCfg.ActionParallelism).
		Int("runner.verification_parallelism", runnerCfg.VerificationParallelism).
		Int("runner.monitor_interval_seconds", runnerCfg.MonitorIntervalSeconds).
		Msg("并发配置")
	// LLM 调用装配：单步看门狗（挂死的调用会阻塞 agent 事件循环）+ llm_invocation 审计（含失败）。
	// 看门狗每次调用现读热配置 runtime.step_llm_timeout_seconds，读失败回落部署值。
	stepLLMTimeout := time.Duration(cfg.Runtime.StepLLMTimeoutSeconds) * time.Second
	heartbeats := runner.NewHeartbeats()
	router.SetProviderWrapper(func(p llm.Provider) llm.Provider {
		return runner.ModelCallChain(p, heartbeats, llminvocation.UsageSink{Store: st.calls}, stepLLMTimeout,
			func(ctx context.Context) time.Duration {
				rt, err := sysSettings.Runtime(ctx)
				if err != nil {
					return 0
				}
				return time.Duration(rt.StepLLMTimeoutSeconds) * time.Second
			})
	})

	creds := credential.NewRedis(rdb)
	h := &runner.Handler{
		AgentRuns:       st.agentRuns,
		Heartbeats:      heartbeats,
		Tasks:           st.tasks,
		Findings:        st.findings,
		Sandbox:         sandbox.NewClient(cfg.Sandbox.URL, cfg.Sandbox.Token),
		SandboxListener: resolveSandboxContract(cfg, logger),
		Corpus:          kb,
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
		Distiller:       distill.New(router, st.graph, st.tasks, kb, logger),
		DistillQueue:    wc,
	}

	mux := worker.NewMux()
	mux.Register(worker.RoleAgent, h.Handle)
	mux.HandleDistill(h.HandleDistill)

	// 后台循环共用一个 ctx：停机第一步取消它，停止接收新流量与各类巡检。
	bgCtx, stopBackground := context.WithCancel(ctx)
	defer stopBackground()

	trafficIngestor, err := ingestor.NewTraffic(bgCtx, ingestor.Deps{
		Redis:         rdb,
		Cfg:           cfg.Ingestor,
		Assignments:   st.assignments,
		Tasks:         st.tasks,
		ProxyStore:    st.proxyStore,
		AgentStore:    st.agentStore,
		AgentRuns:     st.agentRuns,
		Conversations: st.conversations,
		Scenarios:     cfgStore,
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

	startBackgroundLoops(bgCtx, cfg, pool, wc, st, kb, creds, rdb, logger)

	ts := newTaskServer(redisOpt, runnerCfg, logger)
	if err := ts.Start(mux.AsynqMux()); err != nil {
		return fmt.Errorf("启动任务消费者: %w", err)
	}

	hs := newHealthGateway(cfg, trafficIngestor, logger)
	gatewayErr := make(chan error, 1)
	go func() {
		logger.Info().Str("addr", hs.Addr).Msg("runner listening")
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			gatewayErr <- fmt.Errorf("runner 监听 %s: %w", hs.Addr, err)
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-gatewayErr:
	}
	shutdown(logger, runnerCfg, ts, hs, stopBackground)
	return runErr
}

// startBackgroundLoops 启动恢复监控与各类清扫器（随 ctx 取消退出）。
func startBackgroundLoops(ctx context.Context, cfg config.AppConfig, pool *pgxpool.Pool, wc *worker.Client,
	st *stores, kb *corpus.Service, creds *credential.RedisProvider, rdb *redis.Client, logger zerolog.Logger,
) {
	runnerCfg := cfg.Runner
	// 僵尸 run 回收与搁浅任务重投。
	runner.StartRunRecoveryMonitor(ctx, pool, wc, st.agentRuns, st.tasks, st.conversations, convstream.NewPublisher(rdb), logger,
		time.Duration(runnerCfg.RunRecoveryIntervalSeconds)*time.Second,
		time.Duration(runnerCfg.RunRecoveryTimeoutSeconds)*time.Second,
		runnerCfg.RunRecoveryMaxAttempts)

	// 坐实了却没进台账的结果补交付：启动跑一次，此后周期巡检——交付在验证侧尽力而
	// 为（失败只告警），台账缺口由这里按结果节点幂等收账，不等下次重启。
	go func() {
		if err := runner.RedeliverUndeliveredFindings(ctx, st.findings, st.graph, st.tasks, logger); err != nil {
			logger.Warn().Err(err).Msg("坐实结果补交付未全部完成（下轮巡检重试）")
		}
		ticker := time.NewTicker(runner.RedeliverInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := runner.RedeliverUndeliveredFindings(ctx, st.findings, st.graph, st.tasks, logger); err != nil {
					logger.Warn().Err(err).Msg("坐实结果补交付未全部完成（下轮巡检重试）")
				}
			}
		}
	}()

	// 知识库向量补齐：写入时向量化失败、或在配置密钥之前写入的条目。
	runner.StartCorpusIndexLoop(ctx, kb, logger)

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

	// 记录表留存清理：各表到期行每日批删（对应天数配置 0 = 永久留存）。
	runner.StartRetentionSweep(ctx, st.calls, st.toolCalls, st.proxyStore, st.agentStore,
		cfg.LLM.InvocationRetentionDays, cfg.Tools.InvocationRetentionDays, cfg.Traffic.RetentionDays, logger)
}

// newCorpus 装配知识库服务：检索模型密钥每次操作现取（模型配置页热改、跨进程失效），
// 未配置时知识库只做字面检索，不阻塞对话与任务。
func newCorpus(store *corpus.Store, models *llmstore.Store, cipher *cryptx.Cipher, rdb *redis.Client, logger zerolog.Logger) *corpus.Service {
	return corpus.NewService(store, models.RetrievalModels(cipher), retrievalstatus.NewSink(rdb, logger), logger)
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

// newHealthGateway 构造 runner 的 HTTP 服务：healthz 与沙盒流量回灌端点（沙盒浏览器（CDP）抓的
// 流量 POST 到这里，直接入摄入器的进程内队列落 agent_traffic）。
func newHealthGateway(cfg config.AppConfig, trafficIngestor *ingestor.Traffic, logger zerolog.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc(sandbox.TrafficIngestPath, runner.NewIngestHandler(trafficIngestor, cfg.Sandbox.Token, logger))
	return &http.Server{
		Addr:              cfg.Runner.ListenAddr,
		Handler:           mux,
		ReadHeaderTimeout: time.Duration(cfg.Runner.ReadHeaderTimeoutSeconds) * time.Second,
	}
}

// taskServer 是任务消费者：asynq.Server 加上停机时中断在途 run 的能力。
//
// asynq 停机不通知在途 handler：它等宽限期耗尽，再把消息推回队列、取消 handler ctx 并立即
// 返回——进程随即退出，handler 来不及收尾，run 残留 running，只能等僵尸回收并被计作一次出错。
// 若改为取消 asynq 交给 handler 的 ctx，asynq 同样立即放弃 handler、不等它返回。
//
// 一次 run 动辄几十分钟，等它自然结束没有意义；停机中断的语义是「run 落 aborted、任务保持
// 进行中、由恢复监控以新 run 续跑」。所以中断信号走 asynq 之外的 ctx：handler 看到取消、按
// 中断收尾（终态回写、释放沙盒）后返回，asynq 的 Shutdown 等到它返回为止，至多宽限期。
type taskServer struct {
	srv       *asynq.Server
	runs      context.Context
	interrupt context.CancelFunc
}

func newTaskServer(redisOpt asynq.RedisConnOpt, runnerCfg config.RunnerConfig, logger zerolog.Logger) *taskServer {
	runs, interrupt := context.WithCancel(context.Background())
	return &taskServer{
		srv: asynq.NewServer(redisOpt, asynq.Config{
			Concurrency: runnerCfg.Concurrency,
			// 经验沉淀是收尾的后台工作，权重低于 agent run：积压时不挤占任务执行。
			Queues:          map[string]int{worker.QueueAgent: 6, worker.QueueDistill: 1},
			ShutdownTimeout: time.Duration(runnerCfg.DrainTimeoutSeconds) * time.Second,
			// asynq 内部日志（投递重试 / 处理错误）事关任务是否丢失，接进 logx。
			Logger: asynqLogAdapter{logger},
		}),
		runs:      runs,
		interrupt: interrupt,
	}
}

// Start 开始消费；每个 handler 的 ctx 在停机时取消（context.Canceled，即停机中断）。
func (s *taskServer) Start(h asynq.Handler) error {
	return s.srv.Start(asynq.HandlerFunc(func(ctx context.Context, t *asynq.Task) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		defer context.AfterFunc(s.runs, cancel)()
		return h.ProcessTask(ctx, t)
	}))
}

// Shutdown 停止取新任务 → 中断在途 run → 等它们收尾（至多宽限期）。
func (s *taskServer) Shutdown() {
	s.srv.Stop()
	s.interrupt()
	s.srv.Shutdown()
}

// shutdown 优雅关停：停止流量入口与后台循环 → 中断在途 run 并等其收尾 → HTTP 服务。
//
// 沙盒是独立组件，不随 runner 停止；同进程运行（serve）时由编排保证沙盒在 runner 之后才停，
// 收尾中的释放请求仍能送达。
func shutdown(logger zerolog.Logger, runnerCfg config.RunnerConfig, ts *taskServer, hs *http.Server, stopBackground context.CancelFunc) {
	stopBackground()
	ts.Shutdown()
	logger.Info().Msg("在途 run 已按停机中断收尾，任务由恢复监控续跑；中断的经验沉淀留在队列里重试")

	hsCtx, hsCancel := context.WithTimeout(context.Background(), time.Duration(runnerCfg.ShutdownTimeoutSeconds)*time.Second)
	defer hsCancel()
	if err := hs.Shutdown(hsCtx); err != nil {
		logger.Error().Err(err).Msg("runner HTTP 服务停机")
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
