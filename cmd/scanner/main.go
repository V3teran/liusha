// Package main 是 liusha scanner 进程入口（v1.1 redesign）：
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入主 react 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个主 ReAct
//	  3. 主 ReAct 工具集：classify_traffic / scan_vuln(bac) / get_findings / common.{Done,ReadState,WriteFact,WriteIdea,WriteGraph}
//	  4. scan_vuln(bac) → 同进程嵌套 BAC 子 ReAct（NewSubBuilder 装配）
//	  5. healthz HTTP :9090；graceful shutdown
//
//	并发：
//	  asynq.Concurrency=6，6 个 goroutine 并发跑主 ReAct
//	  单主 ReAct 内：runtime tool_calls 并行（多 scan_vuln 自动并发）
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
	bac "github.com/V3teran/liusha/internal/builders/vuln/bac"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/scan"
	"github.com/V3teran/liusha/internal/tools/traffic"
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
	defer func() { _ = calls.Close() }() // 排空 batch buffer，避免最近 ~1s 的审计丢失
	flows := flow.NewStore(pool, flowMaxRequestBody, flowMaxResponseBody)
	creds := credential.NewRedis(rdb)

	// Skill loader：CC 风格渐进加载——
	//   1. Index() 启动扫 skills root，预解析所有 SKILL.md 的 frontmatter（不读 body）
	//   2. Load("vuln/web/bac") 校验 cognitive_map 6 槽位 + done_validator 注册（同步首个 body 进缓存）
	//   3. scan_vuln 每次调用走缓存，0 文件 IO
	skillLoader := skill.NewLoader(cfg.Skills.Root)
	skillNames, err := skillLoader.Index()
	if err != nil {
		logger.Fatal().Err(err).Msg("skill.Index 启动扫描失败")
	}
	logger.Info().Strs("skills", skillNames).Msg("skill index loaded")
	// 启动期 Registry 还没装配，required_actions 校验放到 spawn 时（builder 内传 reg.Has）。
	if _, err := skillLoader.Load("vuln/web/bac", done_validator.IsRegistered, nil); err != nil {
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
	// 用 Instrument 包装：让 distill LLM 调用也写入 llm_call 表（修复 v1.1 bug：原本绕过审计）。
	distillRaw, err := router.For(ctx, "distill")
	if err != nil {
		logger.Fatal().Err(err).Msg("router.For(distill)")
	}
	distillGen := llm.Instrument(
		distillRaw,
		calls,
		llm.CallMeta{RouteKey: "distill"},
		observability.DefaultPricing,
	)
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
	mux.Register(worker.RoleOrchestrator, h.handle)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: asynqConcurrency,
			Queues: map[string]int{
				worker.QueueOrchestrator:     5,
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
//	  classify_traffic / scan_vuln / get_findings
//	  + common.{Done, ReadState, WriteFact, WriteIdea, WriteGraph}
//
//	子 ReAct（scan_vuln）：
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
	mainRaw, err := h.router.For(ctx, "orchestrator")
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}
	mainGen := llm.Instrument(mainRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "orchestrator"},
		h.pricing,
	)

	subRaw, err := h.router.For(ctx, "prober")
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}
	subGen := llm.Instrument(subRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "prober"},
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

	// observer 提前创建：注入主 ReAct + scan_vuln 工具（让其转给子 ReAct 共享判官）
	observer := react.NewLLMObserver(obsGen, h.engagements, eid)

	// 主 ReAct 元工具
	_ = reg.Register(&traffic.ClassifyTraffic{LLM: mainGen})
	// CC 风格：把 skill catalog 注入 ScanVuln —— Description / ParametersJSON 自动列出
	// 所有可用 skill（含每条 frontmatter 的 description），LLM 自主发现。
	// Observer 透传给子 ReAct，让子 ReAct 也享受过程判官（每 5 步评估）。
	_ = reg.Register(&scan.ScanVuln{
		Builders:     map[string]skill.Builder{"vuln/web/bac": bacBuilder},
		EngagementID: eid,
		SubLLM:       subGen,
		Catalog:      h.skillLoader.List(),
		Observer:     observer,
	})
	_ = reg.Register(&traffic.GetFindings{Store: h.findings, EngagementID: eid})

	// middleware：result_compress + done_validate(nil = AlwaysOK)。
	reg.Use(
		middleware.ResultCompress(eid, resultCompressBaseDir),
		middleware.DoneValidate(nil),
	)

	// 自动注入 hint + catalog 到 main system prompt（CC 风格自动发现）。
	e, _ := h.engagements.GetByID(ctx, eid)
	systemPrompt := buildMainSystemPrompt(e, h.skillLoader.List())

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

// buildMainSystemPrompt 主 ReAct 的 system prompt；自动展开 skill catalog + engagement.memory_hints。
//
// CC 风格：catalog 段从 skillLoader.List() 动态生成，加 SKILL.md 文件 → LLM 自动看到。
func buildMainSystemPrompt(e engagement.Engagement, catalog []*skill.Card) string {
	base := `你是渗透测试主 Agent。每个任务对应 1 条 HTTP 流量。

工作流程：
1. classify_traffic 让 LLM 判流量类型 + 输出可能漏洞清单（required_skills）
2. 根据 required_skills 调 scan_vuln(skill_name, flow_id, ...)；
   要测多个漏洞类型可在同一轮返回多个 scan_vuln（runtime 自动 goroutine 并行）
3. 每个 scan_vuln 返回 summary（含 finding 数量），用 get_findings 看详情
4. 必要时 write_fact / write_idea / write_graph 总结
5. done({"reason":"all_skills_done"})

约束：
- 单 task 内最多 spawn 5 个 skill 子任务
- 子任务无依赖时一轮多 spawn，有依赖时分多轮（先看 BAC 结果再决定 RCE）`

	if len(catalog) > 0 {
		base += "\n\n## 可用 Skill（来自 SKILL.md 自动发现）\n"
		// 排序保证 prompt cache 稳定（同 LLM hit 同样字节）
		cards := make([]*skill.Card, len(catalog))
		copy(cards, catalog)
		sort.Slice(cards, func(i, j int) bool { return cards[i].Name < cards[j].Name })
		for _, c := range cards {
			base += fmt.Sprintf("- **%s**: %s\n", c.Name, c.Description)
		}
	}

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
2. scan_vuln 测每种漏洞
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
