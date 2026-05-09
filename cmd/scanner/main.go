// Package main 是 liusha scanner 进程入口（v0024 agentic-lean）：
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入 hunter 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个 hunter agent（单层）
//	  3. healthz HTTP；graceful shutdown
//
// v0024 agentic 重构：删除 orchestrator + sub-react 双层架构，单一 hunter agent
// 接 1 条流量（含 request + response + 凭证 + 已有 finding + hint）自由组合 7 个工具挖漏洞。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/ingestor"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/observability"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools/runners"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"
)

func main() {
	logger := logx.New("scanner")
	ctx := context.Background()

	cfg, err := config.Load(envOr("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}
	scannerCfg := cfg.Scanner

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
	engs := engagement.NewStore(pool).WithLimits(
		cfg.Engagement.MaxMemoryNotesEntries,
		cfg.Engagement.DefaultNotesLimit,
	)
	tasks := agentrun.NewStore(pool).WithCounter(engs)
	finds := finding.NewStore(pool).WithCounter(engs)
	calls := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	lessons := lesson.NewStore(pool)
	if err := lesson.SeedDefaultHints(ctx, lessons); err != nil {
		logger.Warn().Err(err).Msg("seed default hints 失败（业务规则 hint 未入库，agent 将看不到 liusha 自定义判定逻辑）")
	}
	defer func() { _ = calls.Close() }()
	flows := flow.NewStore(pool, scannerCfg.FlowMaxRequestBody, scannerCfg.FlowMaxResponseBody).WithCounter(engs)
	creds := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	pricing := observability.NewPricing(cfg.Pricing)

	// Skill loader：v0024 单一 hunter skill。
	skillLoader := skill.NewLoader(cfg.Skills.Root)
	skillNames, err := skillLoader.Index()
	if err != nil {
		logger.Fatal().Err(err).Msg("skill.Index 启动扫描失败")
	}
	logger.Info().Strs("skills", skillNames).Msg("skill index loaded")
	if _, err := skillLoader.Load("hunter"); err != nil {
		logger.Fatal().Err(err).Msg("load hunter skill")
	}

	// Asynq Client
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	defer wc.Close()

	// LLM Router
	router := llm.NewRouter(llm.NewFactory(cfg))

	// v0024 final agentic：删除 distill hook——hunter agent 用 write_lesson 工具
	// 自决何时沉淀长期经验（省一次 LLM 调用，让 agent 判断"值得不值得记"）。

	// 容器化沙箱执行器（hunter run_command 工具用）
	dockerRunner := runners.NewDockerRunner(runners.WithConcurrency(cfg.Sandbox.RunnerConcurrency))

	// hunter builder（v0024 单一 agent；scanner 启动时构造一次）
	hunterBuilder := hunter.NewBuilder(hunter.Deps{
		Engagements:               engs,
		Findings:                  finds,
		Lessons:                   lessons,
		Credentials:               creds,
		SkillLoader:               skillLoader,
		DockerRunner:              dockerRunner,
		PentoolsImage:             cfg.Sandbox.DefaultImage,
		ScanNetwork:               cfg.Sandbox.ScanNetwork,
		SandboxCfg:                cfg.Sandbox,
		Tenant:                    cfg.Engagement.DefaultTenant,
		ToolExecuteTimeoutSeconds: cfg.Toolruntime.ToolExecuteTimeoutSeconds,
		MaxSteps:                  scannerCfg.MainMaxSteps,
		WatchdogSeconds:           scannerCfg.MainWatchdogSeconds,
		ObserverEverySteps:        cfg.React.ObserverEverySteps,
		DoneForceMaxRejects:       cfg.React.DoneForceMaxRejects,
	})

	// handler
	h := handler{
		tasks:         tasks,
		engagements:   engs,
		findings:      finds,
		flows:         flows,
		calls:         calls,
		cfg:           cfg,
		scannerCfg:    scannerCfg,
		pricing:       pricing,
		router:        router,
		hunterBuilder: hunterBuilder,
		logger:        logger,
	}

	mux := worker.NewMux()
	mux.Register(worker.RoleOrchestrator, h.handle)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: scannerCfg.AsynqConcurrency,
			Queues: map[string]int{
				worker.QueueOrchestrator: scannerCfg.QueueOrchestratorWeight,
				worker.QueueDispatch:     scannerCfg.QueueDispatchWeight,
			},
		},
	)

	// Ingestor goroutine：消费 Redis Stream → 启发式 → 入 main 队列。
	flowCtx, flowCancel := context.WithCancel(context.Background())
	defer flowCancel()

	rotator := engagement.NewRotator(engs, finds, engagement.RotateLimitsFromConfig(cfg.Engagement))

	trafficIngestor, err := ingestor.NewTraffic(flowCtx, ingestor.Deps{
		Redis:    rdb,
		Cfg:      cfg.Ingestor,
		Stream:   cfg.Proxy.StreamName,
		Tenant:   cfg.Engagement.DefaultTenant,
		Engs:     engs,
		Rotator:  rotator,
		Flows:    flows,
		Tasks:    tasks,
		Enqueuer: wc,
		Logger:   logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("new ingestor.traffic")
	}
	go func() {
		if err := trafficIngestor.Run(flowCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("ingestor.traffic exited")
		}
	}()

	// healthz HTTP
	hsMux := http.NewServeMux()
	hsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	hs := &http.Server{
		Addr:              scannerCfg.HealthzAddr,
		Handler:           hsMux,
		ReadHeaderTimeout: time.Duration(cfg.API.ReadHeaderTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info().Str("addr", hs.Addr).Msg("scanner healthz listening")
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
	logger.Info().Str("signal", sig.String()).Msg("scanner shutting down")

	flowCancel()
	asynqDone := make(chan struct{})
	go func() {
		srv.Shutdown()
		close(asynqDone)
	}()
	select {
	case <-asynqDone:
		logger.Info().Msg("asynq shutdown clean")
	case <-time.After(time.Duration(scannerCfg.AsynqShutdownTimeoutSeconds) * time.Second):
		logger.Warn().Int("timeout_seconds", scannerCfg.AsynqShutdownTimeoutSeconds).
			Msg("asynq shutdown timeout — in-flight tasks may be aborted")
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(scannerCfg.ShutdownTimeoutSeconds)*time.Second)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("healthz shutdown")
	}
	logger.Info().Msg("scanner stopped")
}

// handler 持有所有跨任务共享依赖。
type handler struct {
	tasks         *agentrun.Store
	engagements   *engagement.Store
	findings      *finding.Store
	flows         *flow.Store
	calls         *llminvocation.Store
	cfg           config.Config
	scannerCfg    config.ScannerConfig
	pricing       llm.PricingProvider
	router        *llm.Router
	hunterBuilder skill.Builder
	logger        zerolog.Logger
}

// failTask 把错误标记到 task 表。
func (h handler) failTask(ctx context.Context, taskID string, err error) error {
	if setErr := h.tasks.SetError(ctx, taskID, err.Error()); setErr != nil {
		h.logger.Warn().Err(setErr).Str("agent_run_id", taskID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	return err
}

// handle 是单个 hunter task 的处理入口。
func (h handler) handle(ctx context.Context, p worker.Payload) (retErr error) {
	if h.scannerCfg.MainTaskTimeoutSeconds > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(h.scannerCfg.MainTaskTimeoutSeconds)*time.Second)
		defer cancel()
	}

	taskStart := time.Now()
	h.logger.Info().
		Str("agent_run_id", p.TaskID).
		Str("engagement_id", p.EngagementID).
		Str("role", string(p.Role)).
		Msg("asynq task ▶ enter")
	defer func() {
		ev := h.logger.Info()
		if retErr != nil {
			ev = h.logger.Warn().Err(retErr)
		}
		ev.Str("agent_run_id", p.TaskID).
			Str("engagement_id", p.EngagementID).
			Dur("duration", time.Since(taskStart)).
			Msg("asynq task ◀ exit")
	}()

	if err := h.tasks.SetRunning(ctx, p.TaskID); err != nil {
		return err
	}

	var input struct {
		Mode       string          `json:"mode"`
		Entrypoint json.RawMessage `json:"entrypoint"`
	}
	if err := json.Unmarshal(p.Input, &input); err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}

	switch input.Mode {
	case "traffic":
		return h.handleTraffic(ctx, p, input.Entrypoint)
	case "site":
		err := errors.New("site mode 未实现")
		return h.failTask(ctx, p.TaskID, err)
	default:
		err := fmt.Errorf("unknown mode: %s", input.Mode)
		return h.failTask(ctx, p.TaskID, err)
	}
}

// handleTraffic 处理 mode=traffic 的 hunter task（1 流量 → 1 hunter agent）。
//
// v0024 agentic-lean：单层 hunter——拉 flow 完整 raw（请求 + 响应）→ 装配 hunter
// react.Config → 跑 react.Run。不再有 orchestrator + sub-react 双层。
func (h handler) handleTraffic(ctx context.Context, p worker.Payload, entrypoint json.RawMessage) error {
	var ep struct {
		FlowID int64  `json:"flow_id"`
		Host   string `json:"host"`
		URL    string `json:"url"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(entrypoint, &ep); err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}

	tid, eid := p.TaskID, p.EngagementID

	// hunter LLM Generator
	hunterRaw, err := h.router.For(ctx, "hunter")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	hunterGen := llm.Instrument(hunterRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "hunter"},
		h.pricing,
	)

	// observer
	obsRaw, err := h.router.For(ctx, "observer")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	obsGen := llm.Instrument(obsRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "observer"},
		h.pricing,
	)
	observer := react.NewLLMObserver(obsGen, h.engagements, eid)
	observer.ArgsTruncate = h.cfg.React.ObserverArgsTruncate
	observer.ObsTruncate = h.cfg.React.ObserverObsTruncate

	// 拉 flow 完整 raw（请求 + 响应）填 BuilderParams
	fl, err := h.flows.GetByID(ctx, ep.FlowID)
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("flows.GetByID(%d): %w", ep.FlowID, err))
	}

	cfg, err := h.hunterBuilder(ctx, skill.BuilderParams{
		EngagementID:    eid,
		TaskID:          tid,
		FlowID:          ep.FlowID,
		Host:            ep.Host,
		URL:             ep.URL,
		Method:          ep.Method,
		LLM:             hunterGen,
		Observer:        observer,
		RequestHeaders:  fl.RequestHeaders,
		RequestBody:     fl.RequestBody,
		ResponseStatus:  fl.StatusCode,
		ResponseHeaders: fl.ResponseHeaders,
		ResponseBody:    fl.ResponseBody,
	})
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}

	// engagement 中止时让 react.Run 自然停
	cfg.OnAbort = func(c context.Context) (bool, error) {
		eng, err := h.engagements.GetByID(c, eid)
		if err != nil {
			return false, err
		}
		return eng.Status != engagement.StatusActive, nil
	}

	out, err := react.Run(ctx, cfg)
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}

	res, err := json.Marshal(map[string]any{
		"terminate_by":     out.TerminateBy,
		"total_steps":      out.TotalSteps,
		"total_in":         out.TotalUsage.InTokens,
		"total_out":        out.TotalUsage.OutTokens,
		"total_cached":     out.TotalUsage.CachedTokens,
		"observer_hints":   out.ObserverHints,
		"done_force_count": out.DoneForceCount,
	})
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("marshal task result: %w", err))
	}
	return h.tasks.SetDone(ctx, p.TaskID, res)
}

// envOr 读取环境变量；空则返回 def。
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
