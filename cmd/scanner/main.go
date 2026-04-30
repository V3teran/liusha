// Package main 是 liusha scanner 进程入口（v1.1 redesign）：
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入主 react 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个主 ReAct
//	  3. 主 ReAct 工具集：classify_traffic / spawn_skill(bac) / get_findings / common.{Done,ReadState,WriteFact,WriteIdea,WriteGraph}
//	  4. spawn_skill(bac) → 同进程嵌套 BAC 子 ReAct（NewSubBuilder 装配）
//	  5. healthz HTTP :9090；graceful shutdown
//
//	并发：
//	  asynq.Concurrency=6，6 个 goroutine 并发跑主 ReAct
//	  单主 ReAct 内：runtime tool_calls 并行（多 spawn_skill 自动并发）
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

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/graph"
	"github.com/V3teran/liusha/internal/ingestor"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llmcall"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/observability"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/tool/done_validator"
	"github.com/V3teran/liusha/internal/tool/middleware"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/mainreact"
	"github.com/V3teran/liusha/internal/tools/vuln/bac"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"
)

// 默认参数。
const (
	resultCompressBaseDir = "./engagement-store"
	shutdownTimeout       = 5 * time.Second
	flowMaxRequestBody    = 1 << 20
	flowMaxResponseBody   = 2 << 20
	mainMaxSteps          = 30
	mainWatchdogSeconds   = 300
	healthzAddr           = ":9090"
	asynqConcurrency      = 6
)

func main() {
	logger := logx.New("scanner")
	ctx := context.Background()

	cfg, err := config.Load(envOr("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}

	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"), cfg.Postgres.MaxConns, cfg.Postgres.MinConns)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	redisAddr := os.Getenv("LIUSHA_REDIS_ADDR")
	rdb, err := db.NewRedis(ctx, redisAddr)
	if err != nil {
		logger.Fatal().Err(err).Msg("redis")
	}
	defer rdb.Close()

	// Stores。
	tasks := task.NewStore(pool)
	engs := engagement.NewStore(pool)
	finds := finding.NewStore(pool)
	graphs := graph.NewStore(pool)
	calls := llmcall.NewStore(pool)
	flows := flow.NewStore(pool, flowMaxRequestBody, flowMaxResponseBody)
	creds := credential.NewRedis(rdb)

	// Skill loader 启动校验：BAC SKILL.md cognitive_map 6 槽位 + done_validator key 注册。
	skillLoader := skill.NewLoader(cfg.Skills.Root)
	if _, err := skillLoader.Load("vuln/web/bac", done_validator.IsRegistered); err != nil {
		logger.Fatal().Err(err).Msg("load BAC skill")
	}

	// Replay engine（BAC 子 ReAct 用）。
	replayEngine := replay.NewEngine(nil)

	// Asynq Client（消费侧不入队，但留给将来 dispatch / 重试用）。
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	defer wc.Close()

	// LLM Router（T11：Generator 无状态，ClientPool 共享 HTTP client）。
	router := llm.NewRouter(llm.NewFactory(cfg))

	// Distill hook（finding 命中 → light_provider 蒸馏 → memory_hints）。
	distillGen, err := router.For(ctx, "distill")
	if err != nil {
		logger.Fatal().Err(err).Msg("router.For(distill)")
	}
	finds.OnSaved(react.NewDistillHook(distillGen, engs))

	// 主 ReAct handler。
	h := handler{
		tasks:        tasks,
		engagements:  engs,
		findings:     finds,
		graphs:       graphs,
		calls:        calls,
		flows:        flows,
		creds:        creds,
		replayEngine: replayEngine,
		skillLoader:  skillLoader,
		cfg:          cfg,
		pricing:      observability.DefaultPricing,
		router:       router,
		logger:       logger,
	}

	mux := worker.NewMux()
	mux.Register(worker.RoleMain, h.handle)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: asynqConcurrency,
			Queues: map[string]int{
				worker.QueueMain:     5,
				worker.QueueDispatch: 1,
			},
		},
	)

	// Ingestor goroutine：消费 Redis Stream（liusha:flow_events） → 启发式 → 入 main 队列。
	flowCtx, flowCancel := context.WithCancel(context.Background())
	defer flowCancel()

	trafficIngestor, err := ingestor.NewTraffic(flowCtx, ingestor.Deps{
		Redis:    rdb,
		Engs:     engs,
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
	hs := &http.Server{Addr: healthzAddr, Handler: hsMux, ReadHeaderTimeout: 5 * time.Second}

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

	// 关停顺序：先停 ingestor（不再产新 task）→ asynq 收尾（阻塞等 in-flight task）→ healthz。
	flowCancel()
	srv.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("healthz shutdown")
	}
	logger.Info().Msg("scanner stopped")
}

// handler 持有所有跨任务共享依赖；handle() 内每个任务建独立 Registry + Generator。
type handler struct {
	tasks        *task.Store
	engagements  *engagement.Store
	findings     *finding.Store
	graphs       *graph.Store
	calls        *llmcall.Store
	flows        *flow.Store
	creds        credential.Provider
	replayEngine *replay.Engine
	skillLoader  *skill.Loader
	cfg          config.Config
	pricing      llm.PricingProvider
	router       *llm.Router
	logger       zerolog.Logger
}

// handle 是单个主 ReAct task 的处理入口。
//
// payload.Input = {"mode":"traffic"|"site","entrypoint":{...}}。
func (h handler) handle(ctx context.Context, p worker.Payload) error {
	if err := h.tasks.SetRunning(ctx, p.TaskID); err != nil {
		return err
	}

	var input struct {
		Mode       string          `json:"mode"`
		Entrypoint json.RawMessage `json:"entrypoint"`
	}
	if err := json.Unmarshal(p.Input, &input); err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}

	switch input.Mode {
	case "traffic":
		return h.handleTraffic(ctx, p, input.Entrypoint)
	case "site":
		err := errors.New("site mode 未实现（v1.5）")
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	default:
		err := fmt.Errorf("unknown mode: %s", input.Mode)
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}
}

// handleTraffic 处理 mode=traffic 的主 ReAct（1 流量 → 1 主 ReAct）。
//
//	工具集（8 个）：
//	  classify_traffic / spawn_skill / get_findings
//	  + common.{Done, ReadState, WriteFact, WriteIdea, WriteGraph}
//
//	子 ReAct（spawn_skill）：
//	  bac → bac.NewSubBuilder（装配 BAC 工具集 + SKILL.md system prompt + BACValidator）
func (h handler) handleTraffic(ctx context.Context, p worker.Payload, entrypoint json.RawMessage) error {
	var ep struct {
		FlowID int64  `json:"flow_id"`
		Host   string `json:"host"`
		URL    string `json:"url"`
		Method string `json:"method"`
	}
	if err := json.Unmarshal(entrypoint, &ep); err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}

	tid, eid := p.TaskID, p.EngagementID

	// 主 + 子 LLM Generator（T11：每 task 新建无状态实例）。
	mainRaw, err := h.router.For(ctx, "react_main")
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}
	mainGen := llm.Instrument(mainRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "react_main"},
		h.pricing,
	)

	subRaw, err := h.router.For(ctx, "react_main")
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}
	subGen := llm.Instrument(subRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "react_skill"},
		h.pricing,
	)

	obsRaw, err := h.router.For(ctx, "observer")
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}
	obsGen := llm.Instrument(obsRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "observer"},
		h.pricing,
	)

	// 子 ReAct SkillBuilder。
	bacBuilder := bac.NewSubBuilder(bac.SubBuilderDeps{
		Engagements: h.engagements,
		Findings:    h.findings,
		Credentials: h.creds,
		Flows:       h.flows,
		Replay:      h.replayEngine,
		SkillLoader: h.skillLoader,
	})

	// 主 ReAct 工具集。
	reg := tool.NewRegistry()
	_ = reg.Register(common.Done{})
	_ = reg.Register(&common.ReadState{Store: h.engagements, EngagementID: eid})
	_ = reg.Register(&common.WriteFact{Store: h.engagements, EngagementID: eid})
	_ = reg.Register(&common.WriteIdea{Store: h.engagements, EngagementID: eid})
	_ = reg.Register(&common.WriteGraph{Store: h.graphs, EngagementID: eid})

	// mainreact 元工具
	_ = reg.Register(&mainreact.ClassifyTraffic{LLM: mainGen})
	_ = reg.Register(&mainreact.SpawnSkill{
		Builders:     map[string]mainreact.SkillBuilder{"bac": bacBuilder},
		EngagementID: eid,
		SubLLM:       subGen,
	})
	_ = reg.Register(&mainreact.GetFindings{Store: h.findings, EngagementID: eid})

	// middleware：result_compress + done_validate(nil = AlwaysOK)。
	reg.Use(
		middleware.ResultCompress(eid, resultCompressBaseDir),
		middleware.DoneValidate(nil),
	)

	observer := react.NewLLMObserver(obsGen, h.engagements, eid)

	// 自动注入 hint 到 system prompt。
	e, _ := h.engagements.GetByID(ctx, eid)
	systemPrompt := buildMainSystemPrompt(e)

	out, err := react.Run(ctx, react.Config{
		LLM:                mainGen,
		Actions:            reg,
		Budget:             react.Budget{MaxSteps: mainMaxSteps, WatchdogSeconds: mainWatchdogSeconds},
		SystemPrompt:       systemPrompt,
		UserPrompt:         buildMainUserPrompt(ep),
		Observer:           observer,
		ObserverEverySteps: 5,
		OnAbort: func(c context.Context) (bool, error) {
			eng, err := h.engagements.GetByID(c, eid)
			if err != nil {
				return false, err
			}
			return eng.Status != engagement.StatusActive, nil
		},
	})
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}

	res, _ := json.Marshal(map[string]any{
		"terminate_by":     out.TerminateBy,
		"total_steps":      out.TotalSteps,
		"total_in":         out.TotalUsage.InTokens,
		"total_out":        out.TotalUsage.OutTokens,
		"total_cached":     out.TotalUsage.CachedTokens,
		"observer_hints":   out.ObserverHints,
		"done_force_count": out.DoneForceCount,
	})
	return h.tasks.SetDone(ctx, p.TaskID, res)
}

// buildMainSystemPrompt 主 ReAct 的 system prompt；自动注入 engagement.memory_hints。
func buildMainSystemPrompt(e engagement.Engagement) string {
	base := `你是渗透测试主 Agent。每个任务对应 1 条 HTTP 流量。

工作流程：
1. classify_traffic 让 LLM 判流量类型 + 输出可能漏洞清单（required_skills）
2. 根据 required_skills 调 spawn_skill(skill_name, flow_id, ...)；
   要测多个漏洞类型可在同一轮返回多个 spawn_skill（runtime 自动 goroutine 并行）
3. 每个 spawn_skill 返回 summary（含 finding 数量），用 get_findings 看详情
4. 必要时 write_fact / write_idea / write_graph 总结
5. done({"reason":"all_skills_done"})

约束：
- 单 task 内最多 spawn 5 个 skill 子任务
- 子任务无依赖时一轮多 spawn，有依赖时分多轮（先看 BAC 结果再决定 RCE）`

	hints := extractHints(e.MemoryHints)
	if len(hints) > 0 {
		base += "\n\n[历史经验提示（仅供参考）]\n"
		for _, h := range hints {
			base += "- " + h + "\n"
		}
	}
	return base
}

// extractHints 解析 engagement.memory_hints jsonb 字段，提取 content 列表。
func extractHints(hintsJSON []byte) []string {
	if len(hintsJSON) == 0 {
		return nil
	}
	var data struct {
		Hints []struct {
			Content string `json:"content"`
		} `json:"hints"`
	}
	if err := json.Unmarshal(hintsJSON, &data); err != nil {
		return nil
	}
	out := make([]string, 0, len(data.Hints))
	for _, h := range data.Hints {
		if h.Content != "" {
			out = append(out, h.Content)
		}
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
2. spawn_skill 测每种漏洞
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
