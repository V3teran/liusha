// Package main 是 liusha scanner 进程入口（v1.1 redesign）：
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入主 react 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个主 ReAct
//	  3. 主 ReAct 工具集：classify_traffic / delegate(bac) / get_findings / common.{Done,ReadState,WriteFact,WriteIdea,WriteGraph}
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
	"github.com/V3teran/liusha/internal/toolfx"
	"github.com/V3teran/liusha/internal/toolfx/middleware"
	bac "github.com/V3teran/liusha/internal/builders/vuln/bac"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/delegate"
	"github.com/V3teran/liusha/internal/tools/traffic"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
	"github.com/rs/zerolog"
)

// scannerDefaults 是 ScannerConfig 缺字段时的回退默认。
//
// 选 0 值 fallback（而非 yaml 必填校验）让 dev 模式 yaml 可省略 scanner 节直接跑。
var scannerDefaults = config.ScannerConfig{
	MainMaxSteps:           30,
	MainWatchdogSeconds:    300,
	AsynqConcurrency:       6,
	ShutdownTimeoutSeconds: 5,
	HealthzAddr:            ":9090",
	ResultCompressDir:      "./engagement-store",
	FlowMaxRequestBody:     1 << 20, // 1 MiB
	FlowMaxResponseBody:    2 << 20, // 2 MiB
}

// applyScannerDefaults 把 cfg.Scanner 的零值字段补默认（per-field fallback，而非整段 fallback）。
func applyScannerDefaults(c config.ScannerConfig) config.ScannerConfig {
	if c.MainMaxSteps == 0 {
		c.MainMaxSteps = scannerDefaults.MainMaxSteps
	}
	if c.MainWatchdogSeconds == 0 {
		c.MainWatchdogSeconds = scannerDefaults.MainWatchdogSeconds
	}
	if c.AsynqConcurrency == 0 {
		c.AsynqConcurrency = scannerDefaults.AsynqConcurrency
	}
	if c.ShutdownTimeoutSeconds == 0 {
		c.ShutdownTimeoutSeconds = scannerDefaults.ShutdownTimeoutSeconds
	}
	if c.HealthzAddr == "" {
		c.HealthzAddr = scannerDefaults.HealthzAddr
	}
	if c.ResultCompressDir == "" {
		c.ResultCompressDir = scannerDefaults.ResultCompressDir
	}
	if c.FlowMaxRequestBody == 0 {
		c.FlowMaxRequestBody = scannerDefaults.FlowMaxRequestBody
	}
	if c.FlowMaxResponseBody == 0 {
		c.FlowMaxResponseBody = scannerDefaults.FlowMaxResponseBody
	}
	return c
}

func main() {
	logger := logx.New("scanner")
	ctx := context.Background()

	cfg, err := config.Load(envOr("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}
	scannerCfg := applyScannerDefaults(cfg.Scanner)

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
	flows := flow.NewStore(pool, scannerCfg.FlowMaxRequestBody, scannerCfg.FlowMaxResponseBody)
	creds := credential.NewRedis(rdb)

	// Skill loader：CC 风格渐进加载——
	//   1. Index() 启动扫 skills root，预解析所有 SKILL.md 的 frontmatter（不读 body）
	//   2. Load("vuln-web-bac") 预热（同步首个 body 进缓存）
	//   3. delegate 每次调用走缓存，0 文件 IO
	skillLoader := skill.NewLoader(cfg.Skills.Root)
	skillNames, err := skillLoader.Index()
	if err != nil {
		logger.Fatal().Err(err).Msg("skill.Index 启动扫描失败")
	}
	logger.Info().Strs("skills", skillNames).Msg("skill index loaded")
	// 启动期预热：Load 一次让 BAC SKILL.md 进 cardCache（spawn 时 0 文件 IO）。
	if _, err := skillLoader.Load("vuln-web-bac"); err != nil {
		logger.Fatal().Err(err).Msg("load BAC skill")
	}
	// classify-traffic 是 orchestrator 内部 prompt（非可 delegate 的子 skill）：
	// 由 ClassifyTraffic 工具内部 Load body 当 prompt 用。预热避免首次调用文件 IO。
	if _, err := skillLoader.Load("classify-traffic"); err != nil {
		logger.Fatal().Err(err).Msg("load classify-traffic skill")
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
		scannerCfg:   scannerCfg,
		pricing:      observability.DefaultPricing,
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
	hs := &http.Server{Addr: scannerCfg.HealthzAddr, Handler: hsMux, ReadHeaderTimeout: 5 * time.Second}

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
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Duration(scannerCfg.ShutdownTimeoutSeconds)*time.Second)
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
	scannerCfg   config.ScannerConfig
	pricing      llm.PricingProvider
	router       *llm.Router
	logger       zerolog.Logger
}

// failTask 把错误标记到 task 表（SetError 失败不传播），返回原 err 链便于 caller `return`。
//
// 统一收口"出错时打 task 状态 + 返回 err"两步，避免每个 error path 重复 8 行模板。
func (h handler) failTask(ctx context.Context, taskID string, err error) error {
	_ = h.tasks.SetError(ctx, taskID, err.Error())
	return err
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
//	  + common.{Done, ReadState, WriteFact, WriteIdea, WriteGraph}
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
		Engagements:       h.engagements,
		Findings:          h.findings,
		Credentials:       h.creds,
		Flows:             h.flows,
		Replay:            h.replayEngine,
		SkillLoader:       h.skillLoader,
		ResultCompressDir: h.scannerCfg.ResultCompressDir,
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
	mustReg(&common.ReadState{Store: h.engagements, EngagementID: eid})
	mustReg(&common.WriteFact{Store: h.engagements, EngagementID: eid})
	mustReg(&common.WriteIdea{Store: h.engagements, EngagementID: eid})
	mustReg(&common.WriteGraph{Store: h.graphs, EngagementID: eid})

	// observer 提前创建：注入主 ReAct + delegate 工具（让其转给子 ReAct 共享判官）
	observer := react.NewLLMObserver(obsGen, h.engagements, eid)

	// 主 ReAct 元工具
	// classify_traffic 工具持 Flows + Loader：内部按 flow_id 拉完整流量并智能截断后调 LLM，
	// 调用方只需传 flow_id，避免主 LLM "瞎传 headers/body 字段"。
	mustReg(&traffic.ClassifyTraffic{
		LLM:    mainGen,
		Flows:  h.flows,
		Loader: h.skillLoader,
	})
	// CC 风格：把 skill catalog 注入 Delegate —— Description / ParametersJSON 自动列出
	// 所有可用 skill（含每条 frontmatter 的 description），LLM 自主发现。
	// Observer 透传给子 ReAct，让子 ReAct 也享受过程判官（每 5 步评估）。
	//
	// 过滤 classify-traffic：它是 orchestrator 内部 prompt（被 ClassifyTraffic 工具消费），
	// 不是可 delegate 的子 skill；混进 catalog 会让主 LLM 误派任务。
	delegateCatalog := filterDelegateCatalog(h.skillLoader.List())
	mustReg(&delegate.Delegate{
		Builders:     map[string]skill.Builder{"vuln-web-bac": bacBuilder},
		EngagementID: eid,
		SubLLM:       subGen,
		Catalog:      delegateCatalog,
		Observer:     observer,
	})
	mustReg(&traffic.GetFindings{Store: h.findings, EngagementID: eid})
	if regErr != nil {
		return h.failTask(ctx, p.TaskID, regErr)
	}

	// middleware：result_compress + done_validate(nil = AlwaysOK)。
	reg.Use(
		middleware.ResultCompress(eid, h.scannerCfg.ResultCompressDir),
		middleware.DoneValidate(nil),
	)

	// 自动注入 hint + catalog 到 main system prompt（CC 风格自动发现）。
	// catalog 用 filterDelegateCatalog 过滤后的版本（与 delegate 工具看到的一致）。
	e, _ := h.engagements.GetByID(ctx, eid)
	systemPrompt := buildMainSystemPrompt(e, delegateCatalog)

	out, err := react.Run(ctx, react.Config{
		LLM:                mainGen,
		Actions:            reg,
		Budget:             react.Budget{MaxSteps: h.scannerCfg.MainMaxSteps, WatchdogSeconds: h.scannerCfg.MainWatchdogSeconds},
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

// buildMainSystemPrompt 主 ReAct 的 system prompt；自动展开 skill catalog + engagement.memory_hints。
//
// CC 风格：catalog 段从 skillLoader.List() 动态生成，加 SKILL.md 文件 → LLM 自动看到。
func buildMainSystemPrompt(e engagement.Engagement, catalog []*skill.Card) string {
	base := `你是渗透测试主 Agent。每个任务对应 1 条 HTTP 流量。

工作流程：
1. classify_traffic(flow_id) → 拿 JSON：
   {operation, resource_scope, attack_surfaces, carries_auth,
    credential_locations, required_skills, reasoning}
2. 短路判断：
   - required_skills 为空（公开接口 / 无认证 / 无攻击面）→ 直接
     done({"reason":"no_required_skills"})，不要 delegate
3. 否则按 required_skills 调 delegate(skill, flow_id, host,
   credential_locations=<上一步的 credential_locations 原样透传>)；
   要测多个漏洞类型可在同一轮返回多个 delegate（runtime 自动 goroutine 并行）。
   credential_locations 必须透传——子 ReAct 用它构造带占位 token 的 anonymous 假认证。
4. 每个 delegate 返回 summary（含 finding 数量），用 get_findings 看详情
5. 必要时 write_fact / write_idea / write_graph 总结
6. done({"reason":"all_skills_done"})

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

// filterDelegateCatalog 把内部 prompt skill（如 classify-traffic）从 catalog 中剔除，
// 避免主 LLM 误以为它们是可 delegate 的子 skill。
//
// 当前过滤名单是硬编码（仅 classify-traffic 一个）；如未来新增更多内部 prompt skill，
// 可改为按 frontmatter 字段（如 internal:true）过滤。
func filterDelegateCatalog(in []*skill.Card) []*skill.Card {
	out := make([]*skill.Card, 0, len(in))
	for _, c := range in {
		if c == nil {
			continue
		}
		if c.Name == "classify-traffic" {
			continue
		}
		out = append(out, c)
	}
	return out
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
