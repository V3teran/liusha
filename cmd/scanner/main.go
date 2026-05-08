// Package main 是 liusha scanner 进程入口（v1.1 redesign）：
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入主 react 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个主 ReAct
//	  3. 主 ReAct 工具集：classify_traffic / delegate(bac) / get_findings / common.{Done,ReadState,TakeNote,WriteGraph}
//	  4. delegate(bac) → 同进程嵌套 BAC 子 ReAct（NewSubBuilder 装配）
//	  5. healthz HTTP :9090；graceful shutdown
//
//	并发：
//	  asynq.Concurrency=6，6 个 goroutine 并发跑主 ReAct
//	  单主 ReAct 内：runtime tool_calls 并行（多 delegate 自动并发）
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/builder/vuln"
	bac "github.com/V3teran/liusha/internal/builder/vuln/bac"
	sqlibuilder "github.com/V3teran/liusha/internal/builder/vuln/sqli"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/flowfacts"
	"github.com/V3teran/liusha/internal/graph"
	"github.com/V3teran/liusha/internal/ingestor"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/observability"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/toolruntime"
	"github.com/V3teran/liusha/internal/toolruntime/middleware"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/delegate"
	"github.com/V3teran/liusha/internal/tools/runners"
	"github.com/V3teran/liusha/internal/tools/traffic"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"
)

// 默认值已由 config.ApplyDefaults 在 Load 内部统一兜底，scanner 入口不再做 per-field fallback。

func main() {
	logger := logx.New("scanner")
	ctx := context.Background()

	cfg, err := config.Load(envOr("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}
	scannerCfg := cfg.Scanner

	// 在装配 BAC/SQLi 子 ReAct 之前一次性把 yaml 漏洞参数生效（var 覆盖）。
	vuln.Configure(cfg.Vuln)

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

	// Stores。装配顺序：先 engs（被其它 3 个 store 注入为 counter），再 tasks/finds/flows。
	engs := engagement.NewStore(pool).WithLimits(
		cfg.Engagement.MaxMemoryNotesEntries,
		cfg.Engagement.DefaultNotesLimit,
	)
	tasks := agentrun.NewStore(pool).WithCounter(engs)
	finds := finding.NewStore(pool).WithCounter(engs)
	graphs := graph.NewStore(pool)
	calls := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	lessons := lesson.NewStore(pool)
	facts := flowfacts.NewStore(pool)
	defer func() { _ = calls.Close() }() // 排空 batch buffer，避免最近 ~1s 的审计丢失
	flows := flow.NewStore(pool, scannerCfg.FlowMaxRequestBody, scannerCfg.FlowMaxResponseBody).WithCounter(engs)
	creds := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	pricing := observability.NewPricing(cfg.Pricing)

	// Skill loader：CC 风格渐进加载——
	//   1. Index() 启动扫 skills root，预解析所有 SKILL.md 的 frontmatter（不读 body）
	//   2. Load("vuln/web/bac") 预热（同步首个 body 进缓存）
	//   3. delegate 每次调用走缓存，0 文件 IO
	skillLoader := skill.NewLoader(cfg.Skills.Root)
	skillNames, err := skillLoader.Index()
	if err != nil {
		logger.Fatal().Err(err).Msg("skill.Index 启动扫描失败")
	}
	logger.Info().Strs("skills", skillNames).Msg("skill index loaded")
	// 启动期预热：Load 一次让 BAC SKILL.md 进 cardCache（spawn 时 0 文件 IO）。
	if _, err := skillLoader.Load("vuln/web/bac"); err != nil {
		logger.Fatal().Err(err).Msg("load BAC skill")
	}
	if _, err := skillLoader.Load("vuln/web/sqli"); err != nil {
		logger.Fatal().Err(err).Msg("load SQLi skill")
	}
	// classify-traffic 是 orchestrator 内部 prompt（非可 delegate 的子 skill）：
	// 由 ClassifyTraffic 工具内部 Load body 当 prompt 用。预热避免首次调用文件 IO。
	if _, err := skillLoader.Load("classify-traffic"); err != nil {
		logger.Fatal().Err(err).Msg("load classify-traffic skill")
	}

	// Replay engine（BAC 子 ReAct 用）。
	// 显式带 timeout 的 HTTP client：目标服务挂或慢响应不会阻塞 ReplayMatrix 全批；
	// timeout=0 时退化到 http.DefaultClient（无 timeout，不推荐生产使用）。
	var replayHTTPClient *http.Client
	if to := cfg.Replay.HTTPTimeoutSeconds; to > 0 {
		replayHTTPClient = &http.Client{Timeout: time.Duration(to) * time.Second}
	}
	replayEngine := replay.NewEngine(replayHTTPClient, cfg.Replay.Concurrency)

	// Asynq Client（消费侧不入队，但留给将来 dispatch / 重试用）。
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	defer wc.Close()

	// LLM Router（T11：Generator 无状态，ClientPool 共享 HTTP client）。
	router := llm.NewRouter(llm.NewFactory(cfg))

	// LessonExtract hook（finding 命中 → light_provider 提取 → lesson 跨 engagement 知识库）。
	// 用 Instrument 包装：让 lesson_extract LLM 调用也写入 llm_call 表（修复 v1.1 bug：原本绕过审计）。
	extractRaw, err := router.For(ctx, "lesson_extract")
	if err != nil {
		logger.Fatal().Err(err).Msg("router.For(lesson_extract)")
	}
	extractGen := llm.Instrument(
		extractRaw,
		calls,
		llm.CallMeta{RouteKey: "lesson_extract"},
		pricing,
	)
	// 首次发现 → 提取写 lesson（仅 LLM 一次调用）
	finds.OnSaved(react.NewLessonExtractHook(extractGen, lessons, cfg.Lesson))
	// 重发现 → 给对应 lesson hit_count+1（无 LLM；体现经验被复用）
	finds.OnReSaved(react.NewLessonTouchHook(lessons, cfg.Lesson))

	// 主 ReAct handler。
	h := handler{
		tasks:        tasks,
		engagements:  engs,
		findings:     finds,
		lessons:      lessons,
		graphs:       graphs,
		calls:        calls,
		flows:        flows,
		facts:        facts,
		creds:        creds,
		replayEngine: replayEngine,
		skillLoader:  skillLoader,
		cfg:          cfg,
		scannerCfg:   scannerCfg,
		pricing:      pricing,
		router:       router,
		logger:       logger,
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

	// Ingestor goroutine：消费 Redis Stream（liusha:flow_events） → 启发式 → 入 main 队列。
	flowCtx, flowCancel := context.WithCancel(context.Background())
	defer flowCancel()

	// proxy 模式 engagement 滚动（24h / 1MiB / 100 finding 任一触发）；
	// 整站模式（mode != proxy）由 Rotator 直通，不轮转。
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

	// healthz HTTP。
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

	// 关停顺序：先停 ingestor（不再产新 task）→ asynq 收尾（阻塞等 in-flight task，
	// 加 AsynqShutdownTimeoutSeconds 超时熔断防卡死）→ healthz。
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

// handler 持有所有跨任务共享依赖；handle() 内每个任务建独立 Registry + Generator。
type handler struct {
	tasks        *agentrun.Store
	engagements  *engagement.Store
	findings     *finding.Store
	lessons      *lesson.Store
	graphs       *graph.Store
	calls        *llminvocation.Store
	flows        *flow.Store
	facts        *flowfacts.Store
	creds        credential.Provider
	replayEngine *replay.Engine
	skillLoader  *skill.Loader
	cfg          config.Config
	scannerCfg   config.ScannerConfig
	pricing      llm.PricingProvider
	router       *llm.Router
	logger       zerolog.Logger
}

// failTask 把错误标记到 task 表（SetError 失败仅 warn 不传播），返回原 err 链便于 caller `return`。
func (h handler) failTask(ctx context.Context, taskID string, err error) error {
	if setErr := h.tasks.SetError(ctx, taskID, err.Error()); setErr != nil {
		h.logger.Warn().Err(setErr).Str("agent_run_id", taskID).
			Msg("SetError 失败（task 留在 running，原始错误已透传给 caller）")
	}
	return err
}

// handle 是单个主 ReAct task 的处理入口。
//
// payload.Input = {"mode":"traffic"|"site","entrypoint":{...}}。
func (h handler) handle(ctx context.Context, p worker.Payload) (retErr error) {
	// 主 ReAct 任务整体超时（防 sqlmap 卡死等 in-flight 路径无限挂起）。
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
		err := errors.New("site mode 未实现（v1.5）")
		return h.failTask(ctx, p.TaskID, err)
	default:
		err := fmt.Errorf("unknown mode: %s", input.Mode)
		return h.failTask(ctx, p.TaskID, err)
	}
}

// handleTraffic 处理 mode=traffic 的主 ReAct（1 流量 → 1 主 ReAct）。
//
//	工具集（8 个）：
//	  classify_traffic / delegate / get_findings
//	  + common.{Done, ReadState, TakeNote, WriteGraph}
//
//	子 ReAct（delegate）：
//	  bac → bac.NewSubBuilder（装配 BAC 工具集 + SKILL.md system prompt + BACValidator）
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

	// 主 + 子 LLM Generator（T11：每 task 新建无状态实例）。
	mainRaw, err := h.router.For(ctx, "orchestrator")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	mainGen := llm.Instrument(mainRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "orchestrator"},
		h.pricing,
	)

	subRaw, err := h.router.For(ctx, "hunter")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	subGen := llm.Instrument(subRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "hunter"},
		h.pricing,
	)

	obsRaw, err := h.router.For(ctx, "observer")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	obsGen := llm.Instrument(obsRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "observer"},
		h.pricing,
	)

	// 子 ReAct SkillBuilder。
	bacBuilder := bac.NewSubBuilder(bac.SubBuilderDeps{
		Engagements:               h.engagements,
		Findings:                  h.findings,
		Lessons:                   h.lessons,
		Credentials:               h.creds,
		Flows:                     h.flows,
		Replay:                    h.replayEngine,
		SkillLoader:               h.skillLoader,
		ProbeCfg:                  h.cfg.Probe,
		AuthKeywords:              h.cfg.Heuristic.AuthKeywords,
		Tenant:                    h.cfg.Engagement.DefaultTenant,
		ToolExecuteTimeoutSeconds: h.cfg.Toolruntime.ToolExecuteTimeoutSeconds,
	})
	sqliBuilder := sqlibuilder.NewSubBuilder(sqlibuilder.SubBuilderDeps{
		Engagements:               h.engagements,
		Findings:                  h.findings,
		Lessons:                   h.lessons,
		Credentials:               h.creds,
		Flows:                     h.flows,
		Replay:                    h.replayEngine,
		SkillLoader:               h.skillLoader,
		ProbeCfg:                  h.cfg.Probe,
		AuthKeywords:              h.cfg.Heuristic.AuthKeywords,
		Tenant:                    h.cfg.Engagement.DefaultTenant,
		ToolExecuteTimeoutSeconds: h.cfg.Toolruntime.ToolExecuteTimeoutSeconds,

		// 重型工具：默认 docker_runner + cfg.Sandbox.DefaultImage。docker 不可用时
		// docker_runner 调用层会返回 err，run_command 工具自身不强制依赖。
		DockerRunner:  runners.NewDockerRunner(runners.WithConcurrency(h.cfg.Sandbox.RunnerConcurrency)),
		PentoolsImage: h.cfg.Sandbox.DefaultImage,
		SandboxCfg:    h.cfg.Sandbox,
		ScanNetwork:   h.cfg.Sandbox.ScanNetwork,
	})

	// 主 ReAct 工具集。
	reg := toolfx.NewRegistry()
	// mustReg 累积 register 错误：任一失败标记 regErr 后续 register 跳过；
	// 全部尝试完后统一返 failTask（一次启动暴露所有重名/工具构造问题）。
	var regErr error
	mustReg := func(a toolfx.Action) {
		if regErr != nil {
			return
		}
		if err := reg.Register(a); err != nil {
			regErr = fmt.Errorf("register %T: %w", a, err)
		}
	}
	mustReg(common.Done{})
	mustReg(&common.ReadState{Store: h.engagements, EngagementID: eid, TaskID: tid})
	mustReg(&common.TakeNote{Store: h.engagements, EngagementID: eid, TaskID: tid})
	mustReg(&common.WriteGraph{Store: h.graphs, EngagementID: eid})

	// observer 提前创建：注入主 ReAct + delegate 工具（让其转给子 ReAct 共享判官）
	observer := react.NewLLMObserver(obsGen, h.engagements, eid)
	observer.ArgsTruncate = h.cfg.React.ObserverArgsTruncate
	observer.ObsTruncate = h.cfg.React.ObserverObsTruncate

	// 主 ReAct 元工具
	// classify_traffic 工具持 Flows + Loader：内部按 flow_id 拉完整流量并智能截断后调 LLM，
	// 调用方只需传 flow_id，避免主 LLM "瞎传 headers/body 字段"。
	//
	// 单独 Instrument 一份：RouteKey="classify_traffic"，让审计粒度清晰区分
	// "orchestrator 决策步" vs "classify_traffic 工具内部 LLM 分析"——之前共用
	// mainGen 时所有 classify 调用被记成 orchestrator role，混淆 cost/调用统计。
	// 共享同一 mainRaw Generator（无状态，ClientPool 复用底层 HTTP client）。
	classifyGen := llm.Instrument(mainRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "classify_traffic"},
		h.pricing,
	)
	mustReg(&traffic.ClassifyTraffic{
		LLM:          classifyGen,
		Flows:        h.flows,
		Loader:       h.skillLoader,
		Cfg:          h.cfg.Classify,
		Facts:        h.facts,
		EngagementID: eid,
	})
	// CC 风格：把 skill catalog 注入 Delegate —— Description / ParametersJSON 自动列出
	// 所有可用 skill（含每条 frontmatter 的 description），LLM 自主发现。
	// Observer 透传给子 ReAct，让子 ReAct 也享受过程判官（每 5 步评估）。
	//
	// 过滤 classify-traffic：它是 orchestrator 内部 prompt（被 ClassifyTraffic 工具消费），
	// 不是可 delegate 的子 skill；混进 catalog 会让主 LLM 误派任务。
	delegateCatalog := filterDelegateCatalog(h.skillLoader.List())
	mustReg(&delegate.Tool{
		Builders: map[string]skill.Builder{
			"vuln/web/bac":  bacBuilder,
			"vuln/web/sqli": sqliBuilder,
		},
		EngagementID: eid,
		SubLLM:       subGen, // 兜底（maker 为 nil 时用）
		Catalog:      delegateCatalog,
		Observer:     observer, // 兜底
		Tasks:        h.tasks,
		// agentic 路线：spawn 时拉完整 flow 详情（headers + body）填进 BuilderParams，
		// 让子 ReAct LLM 在 user prompt 一次性看到完整流量自识别注入点 / 凭证位等。
		Flows: h.flows,
		// 子 ReAct 整体超时（防 hunter 卡死无总上限），从 yaml vuln.sub_task_timeout_seconds 注入。
		SubTaskTimeoutSeconds: h.cfg.Vuln.SubTaskTimeoutSeconds,
		// 现场用 sub-task uuid Instrument，让 hunter/observer 的 llm_call.task_id
		// 真正挂在 sub-task 上（之前都挂在父 orchestrator tid）。
		SubLLMFor: func(subTaskID string) llm.Generator {
			tid := subTaskID
			return llm.Instrument(subRaw, h.calls,
				llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "hunter"},
				h.pricing,
			)
		},
		ObserverFor: func(subTaskID string) react.Observer {
			tid := subTaskID
			subObsGen := llm.Instrument(obsRaw, h.calls,
				llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "observer"},
				h.pricing,
			)
			subObserver := react.NewLLMObserver(subObsGen, h.engagements, eid)
			subObserver.ArgsTruncate = h.cfg.React.ObserverArgsTruncate
			subObserver.ObsTruncate = h.cfg.React.ObserverObsTruncate
			return subObserver
		},
	})
	mustReg(&traffic.GetFindings{Store: h.findings, EngagementID: eid})
	if regErr != nil {
		return h.failTask(ctx, p.TaskID, regErr)
	}

	// middleware（主 ReAct 3 层）：observe（最外层埋点）→ timeout（兜底）→ result_compress（超阈值截断喂 LLM）。
	// 主 ReAct 不写 finding（只 delegate / classify_traffic / get_findings），不需要 done_validate。
	reg.Use(
		middleware.Observe(),
		middleware.Timeout(h.cfg.Toolruntime.ToolExecuteTimeoutSeconds),
		middleware.ResultCompress(
			h.cfg.Toolruntime.ResultCompressThreshold,
			h.cfg.Toolruntime.ResultCompressSnippet,
			h.cfg.Toolruntime.ResultCompressSummary,
		),
	)

	// catalog + system prompt（CC 风格自动发现）；prompt body 从 skills/orchestrator/SKILL.md 加载。
	systemPrompt := buildMainSystemPrompt(h.skillLoader, delegateCatalog)

	out, err := react.Run(ctx, react.Config{
		LLM:                 mainGen,
		Actions:             reg,
		Budget:              react.Budget{MaxSteps: h.scannerCfg.MainMaxSteps, WatchdogSeconds: h.scannerCfg.MainWatchdogSeconds},
		SystemPrompt:        systemPrompt,
		UserPrompt:          buildMainUserPrompt(ep),
		Observer:            observer,
		ObserverEverySteps:  h.cfg.React.ObserverEverySteps,
		DoneForceMaxRejects: h.cfg.React.DoneForceMaxRejects,
		OnAbort: func(c context.Context) (bool, error) {
			eng, err := h.engagements.GetByID(c, eid)
			if err != nil {
				return false, err
			}
			return eng.Status != engagement.StatusActive, nil
		},
	})
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

// buildMainSystemPrompt 主 ReAct 的 system prompt。
//
// v1.2 改造：
//   - base 内容从 skills/orchestrator/SKILL.md 加载（不再硬编码）
//   - 砍 lessons 加载——主 ReAct 是调度员，不需要懂具体 payload，让子 ReAct 自己看 lesson
//   - 仅保留 catalog 动态展开（CC 风格：加 SKILL.md → LLM 自动看到）
//
// v1.3 改造（agentic 哲学修正）：
//   - catalog 展开新增触发元数据 requires_auth / applicable_param_locations
//   - 主 ReAct 据此 + classify_traffic 返回的 facts 自主决策派哪些 delegate
//   - 不再依赖 classify_traffic 输出的 required_skills（已下线）
//
// loader.Load 命中 cache 后 0 IO；首次启动后 base 字符串恒定，prompt cache 稳定。
func buildMainSystemPrompt(loader *skill.Loader, catalog []*skill.Card) string {
	base := defaultMainPrompt
	if card, err := loader.Load("orchestrator"); err == nil && card.Body != "" {
		base = card.Body
	}

	if len(catalog) > 0 {
		base += "\n\n## 可用 Skill（来自 SKILL.md 自动发现）\n"
		cards := make([]*skill.Card, len(catalog))
		copy(cards, catalog)
		sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
		for _, c := range cards {
			base += fmt.Sprintf("- **%s**%s: %s\n", c.Name, formatTriggerMeta(c), c.Description)
		}
	}
	return base
}

// formatTriggerMeta 把 Card 的触发元数据展开成 catalog 行的简短提示。
// 元数据全部缺省 → 返回 ""；有任意字段 → 形如 " (requires_auth=true; params=[query,json])"。
func formatTriggerMeta(c *skill.Card) string {
	if !c.RequiresAuth && len(c.ApplicableParamLocations) == 0 {
		return ""
	}
	parts := make([]string, 0, 2)
	if c.RequiresAuth {
		parts = append(parts, "requires_auth=true")
	}
	if len(c.ApplicableParamLocations) > 0 {
		parts = append(parts, "params=["+strings.Join(c.ApplicableParamLocations, ",")+"]")
	}
	return " (" + strings.Join(parts, "; ") + ")"
}

// defaultMainPrompt 是 skills/orchestrator/SKILL.md 加载失败时的兜底。
// 正常路径不会用到——loader.Load 失败说明部署残缺，但仍返回最小 base 让 ReAct 跑起来。
const defaultMainPrompt = "你是渗透测试主 Agent。看 catalog 与流量事实，自主决定调度。"

// filterDelegateCatalog 把内部 prompt skill 从 catalog 中剔除，
// 避免主 LLM 误以为它们是可 delegate 的子 skill。
//
// 当前过滤：
//   - classify-traffic：是 ClassifyTraffic 工具内部 prompt
//   - orchestrator：本 ReAct 自身的 system prompt，更不能 delegate 给自己
//
// 未来新增更多内部 prompt skill 可改为按 frontmatter 字段（如 internal:true）过滤。
func filterDelegateCatalog(in []*skill.Card) []*skill.Card {
	out := make([]*skill.Card, 0, len(in))
	for _, c := range in {
		if c == nil {
			continue
		}
		if c.Name == "classify-traffic" || c.Name == "orchestrator" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// buildMainUserPrompt 构造主 ReAct 的第一条 user message。
func buildMainUserPrompt(ep struct {
	FlowID int64  `json:"flow_id"`
	Host   string `json:"host"`
	URL    string `json:"url"`
	Method string `json:"method"`
}) string {
	return fmt.Sprintf(`分析 flow_id=%d host=%s %s %s。

立刻按系统步骤调用工具：
1. 先 classify_traffic 拿漏洞清单
2. delegate 测每种漏洞
3. get_findings 看汇总
4. done`, ep.FlowID, ep.Host, ep.Method, ep.URL)
}

// envOr 读取环境变量；空则返回 def。
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
