// Package main 是 liusha scanner 进程入口：装配 config / pg / redis / stores /
// LLM Router + asynq 消费者 + ReAct sniffer 角色 handler + healthz HTTP。
//
// 黑客松借鉴闭环（plan 1 part3 §借鉴增量）：
//   - LLM Router（T21.5）：按 cfg.LLM.Routes 路由 react.main / observer / distill 到不同 provider，
//     外层套 retry + fallback。
//   - Distill hook（T23.5）：finding 写库后 light_provider 蒸馏成 ≤200 字 hint，落 memory_hints。
//   - Action 中间件链（T22.5）：result_compress / loop_detect / done_validate 三层横切。
//   - Observer（T23.5）：每 5 步用 light_provider 判官，决定 keep_going / steer / abort。
//
// plan 2 T5 增量：按 p.Skill 选择 action 集 + skill loader 装载 SKILL.md：
//   - p.Skill == ""           → 顶层 sniffer：7 通用 action + read_window + spawn_subtask
//   - p.Skill == "vuln/web/bac" → BAC 子任务：7 通用 action + bac.Factory 4 个 action + BACValidator
//
// plan 3 T5 增量：mitm 代理 + filter/dedup/aggregator 拆分到独立的 cmd/proxy 进程；
// scanner 仅作为 Asynq 消费者 + ReAct 引擎，故障隔离 + 独立扩缩。
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

	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/tool/middleware"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/vuln/bac"
	"github.com/V3teran/liusha/internal/tool/done_validator"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/graph"
	"github.com/V3teran/liusha/internal/llmcall"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/observability"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
)

// snifferSystemPrompt 是 sniffer 主任务（p.Skill == ""）的固定 system prompt。
//
// BAC 子任务（p.Skill == "vuln/web/bac"）改用 SKILL.md 正文作为 system prompt，
// 由 skill loader 在 handle() 内按需加载。
const snifferSystemPrompt = `你是 sniffer 角色。每次任务输入对应一个 traffic_window。

工作流程：
1. read_window(window_id) → 拿当前窗口里的 N 条 flow 摘要
2. 扫描可疑信号：
   - URL 含 /admin/ /sys/，或参数含 ?uid= /:user_id/ → spawn_subtask(skill="vuln/web/bac", input={"flow_id":<id>,"host":<host>})
3. 在所有可疑 flow 都已 spawn 之后 done({"reason":"window_consumed"})

规则：
- 同一窗口内每个 flow 只 spawn 一次
- 单窗口最多 spawn 5 个子任务（避免炸开）
- 不要直接判漏洞，那是 BAC skill 的工作
- 没可疑就 done({"reason":"no_suspicious"})`

// resultCompressBaseDir 是 result_compress 中间件落盘的根目录。
// docker compose 中由 volume 挂在容器外，便于人类追溯大型 evidence。
const resultCompressBaseDir = "./engagement-store"

// shutdownTimeout 是 healthz HTTP 优雅关闭的超时；asynq.Shutdown 自身阻塞直到 in-flight 任务结束。
const shutdownTimeout = 5 * time.Second

// flow body 截断阈值——scanner 只读 flow（BAC replay 拿原始 body），写入由 cmd/proxy 负责。
const (
	flowMaxRequestBody  = 1 << 20
	flowMaxResponseBody = 2 << 20
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

	// Stores（pg + redis）
	tasks := task.NewStore(pool)
	engs := engagement.NewStore(pool)
	finds := finding.NewStore(pool)
	graphs := graph.NewStore(pool)
	calls := llmcall.NewStore(pool)
	flows := flow.NewStore(pool, flowMaxRequestBody, flowMaxResponseBody)
	creds := credential.NewRedis(rdb)

	// BAC factory（plan 2 T2）：creds + flows + replay engine。
	replayEngine := replay.NewEngine(nil)
	bacFactory := bac.NewFactory(creds, flows, replayEngine)

	// worker.Client 生产者：BAC 子任务暂时不再有上游派发器（v1.1 sniffer/spawner 已废止），
	// 但 Asynq 消费者仍需要 redis 连接来消费外部入队的任务，保留 Client 以备 T15 改造。
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	defer wc.Close()

	// Skill loader（plan 1 T25）：启动时加载 BAC SKILL.md 校验 cognitive_map 6 槽位
	// + done_validator key 已注册。失败立即 fatal，避免运行期 surprise。
	skillLoader := skill.NewLoader(cfg.Skills.Root)
	if _, err := skillLoader.Load("vuln/web/bac", done_validator.IsRegistered); err != nil {
		logger.Fatal().Err(err).Msg("load BAC skill")
	}

	// T21.5：LLM Router——所有 LLM 调用走 router.For(ctx, role, tools)，
	// 自动按 cfg.LLM.Routes 路由 + retry/fallback。
	router := llm.NewRouter(llm.NewFactory(cfg))

	// T23.5：Distill hook——finding 写库后异步触发，调 light_provider 蒸馏成 hint。
	// 这里 distill 不绑工具（只调 chat），tools=nil；router 内部按 "distill" 路由 + 缓存。
	distillGen, err := router.For(ctx, "distill", nil)
	if err != nil {
		logger.Fatal().Err(err).Msg("router.For(distill)")
	}
	finds.OnSaved(react.NewDistillHook(distillGen, engs))

	mux := worker.NewMux()
	h := snifferHandler{
		tasks:       tasks,
		engagements: engs,
		findings:    finds,
		graphs:      graphs,
		calls:       calls,
		flows:       flows,
		bacFactory:  bacFactory,
		skillLoader: skillLoader,
		cfg:         cfg,
		pricing:     observability.DefaultPricing,
		router:      router,
		budget: react.Budget{
			MaxSteps:        cfg.LLM.MaxSteps,
			MaxTokens:       50_000,
			WatchdogSeconds: 60,
		},
	}
	mux.Register(worker.RoleSniffer, h.handle)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: 4,
			Queues: map[string]int{
				worker.QueueSniffer:  5,
				worker.QueueOperator: 1,
			},
		},
	)

	// v1.1：traffic_window 切窗 + ingestor 消费者全部废止；scanner 仅作为 Asynq 消费者运行。
	// 上游入队由外部触发（API 直接 enqueue 或 proxy 重写后续）；T20 重写 scanner main 时统一调整。

	hsMux := http.NewServeMux()
	hsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	hs := &http.Server{Addr: ":9090", Handler: hsMux, ReadHeaderTimeout: 5 * time.Second}

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

	// 关停顺序：asynq 收尾（阻塞等 in-flight task）→ healthz。
	srv.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("healthz shutdown")
	}
	logger.Info().Msg("scanner stopped")
}

// buildUserPrompt 把 worker.Payload.Input（JSON）解析成明确的中文指令，引导 LLM 立即调工具。
//
// 业务背景：DeepSeek 等 chat 模型对"裸 JSON user prompt + system 指令"容易直接给文本回复
// 而不调工具（terminate_by="no_tool_call" 一步退出）。把意图写成自然语言指令更稳。
//
// sniffer 主任务：读 window_id → 调 read_window 拿 flow → 对每个可疑 flow spawn BAC 子任务。
// BAC 子任务：读 input.flow_id → 调 read_state / fetch_creds / replay → write_finding。
func buildUserPrompt(p worker.Payload) string {
	if p.Skill == "" {
		var in struct {
			WindowID string `json:"window_id"`
		}
		_ = json.Unmarshal(p.Input, &in)
		return fmt.Sprintf(
			`当前任务：分析 traffic_window（id=%s）。

立刻按以下步骤调用工具，不要给文本回答：
1. 调用 read_window，参数 {"window_id":"%s"}，拿到本窗口的 flow 列表（id/method/url/status/host）
2. 对每个可疑 flow（URL 含 /admin/、/sys/，或 query/path 含 uid=、user_id、order/、profile 等身份相关参数），调 spawn_subtask，参数 {"skill":"vuln/web/bac","input":{"flow_id":<flow.id>,"host":"%s"}}
3. 全部 spawn 完成后调 done，参数 {"reason":"window_consumed"}
4. 没可疑就调 done，参数 {"reason":"no_suspicious"}

约束：单窗口最多 5 个子任务；同一 flow 只 spawn 一次。`,
			in.WindowID, in.WindowID, "")
	}
	// BAC / 其它 skill：input 已经包含 flow_id 等业务字段，直接给原文 + 一句指令前缀。
	return fmt.Sprintf("立刻按 SKILL 流程调用工具，不要文本回答。input=%s", string(p.Input))
}

// snifferHandler 持有所有跨任务共享依赖；handle() 内每个任务建独立 Registry + Generator。
//
// 注意 sniffer / BAC 共享一个 handler：handle() 内按 p.Skill 选择 action 集 + system prompt，
// 这样不必为每种 skill 拉一个独立 worker 进程。
type snifferHandler struct {
	tasks       *task.Store
	engagements *engagement.Store
	findings    *finding.Store
	graphs      *graph.Store
	calls       *llmcall.Store
	flows       *flow.Store
	bacFactory  *bac.Factory
	skillLoader *skill.Loader
	cfg         config.Config
	pricing     llm.PricingProvider
	router      *llm.Router // T21.5：多 provider 路由（react.main / observer / distill）
	budget      react.Budget
}

// handle 是单个 task 的处理入口（sniffer 主任务或 BAC 子任务）：
//  1. 任务状态推进 pending → running
//  2. 注册 7 通用 action
//  3. 按 p.Skill 选择附加 action 集 + system prompt + done validator
//  4. 套上中间件链（result_compress / loop_detect / done_validate）
//  5. 通过 router 拿 react.main + observer Generator
//  6. react.Run 跑 ReAct 主循环
//  7. result JSON 落库（含 observer_hints / done_force_count，用于审计）
func (h snifferHandler) handle(ctx context.Context, p worker.Payload) error {
	if err := h.tasks.SetRunning(ctx, p.TaskID); err != nil {
		return err
	}

	reg := tool.NewRegistry()
	_ = reg.Register(common.Done{})
	_ = reg.Register(&common.ReadState{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&common.WriteFact{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&common.WriteIdea{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&common.WriteHint{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&common.WriteFinding{Store: h.findings, EngagementID: p.EngagementID, TaskID: p.TaskID})
	_ = reg.Register(&common.WriteGraph{Store: h.graphs, EngagementID: p.EngagementID})

	// 按 skill 选择 action 集 + system prompt + done validator。
	systemPrompt, doneValidator, err := h.skillSetup(reg, p)
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}

	// T22.5：套上中间件链；done_validator 按 skill 注入（sniffer=nil → AlwaysOK，BAC=BACValidator）。
	// LoopDetect 已砍——MaxSteps + DoneValidator 已是足够的死循环兜底。
	reg.Use(
		middleware.ResultCompress(p.EngagementID, resultCompressBaseDir),
		middleware.DoneValidate(doneValidator),
	)

	// 每个 task 一个全新 Generator（tools 一次绑定，避免跨 goroutine 竞争 BindTools 内部状态）。
	mainGen, err := h.router.For(ctx, "react_main", reg.Schemas())
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}

	tid, eid := p.TaskID, p.EngagementID
	gen := llm.Instrument(mainGen, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "react_main"},
		h.pricing,
	)

	// T23.5：Observer 走 light_provider；不绑 tools（observer 只输出 JSON 决策）。
	obsRaw, err := h.router.For(ctx, "observer", nil)
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}
	obsGen := llm.Instrument(obsRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "observer"},
		h.pricing,
	)
	observer := react.NewLLMObserver(obsGen, h.engagements, eid)

	out, err := react.Run(ctx, react.Config{
		LLM:                gen,
		Actions:            reg,
		Budget:             h.budget,
		SystemPrompt:       systemPrompt,
		UserPrompt:         buildUserPrompt(p),
		Observer:           observer,
		ObserverEverySteps: 5,
		OnAbort: func(c context.Context) (bool, error) {
			e, err := h.engagements.GetByID(c, eid)
			if err != nil {
				return false, err
			}
			return e.Status != engagement.StatusActive, nil
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

// skillSetup 按 p.Skill 注册附加 action 集 + 选 system prompt + 实例化 DoneValidator。
//
//   - p.Skill == ""：顶层 sniffer——附加 read_window + spawn_subtask；done validator 用 nil（AlwaysOK）。
//   - p.Skill == "vuln/web/bac"：BAC 子任务——附加 bac.Factory 4 个 action；
//     system prompt 用 SKILL.md 正文；done validator 用 BACValidator（per-task 实例，绑 eid）。
//   - 其它 skill：返回错误（plan 2 仅支持 BAC；后续 skill 走同样模式扩展）。
func (h snifferHandler) skillSetup(
	reg *tool.Registry,
	p worker.Payload,
) (string, tool.DoneValidator, error) {
	switch p.Skill {
	case "":
		// v1.1：sniffer/window/spawner 全部废止；保留分支占位待 T15 worker 简化时统一改写。
		return snifferSystemPrompt, nil, nil

	case "vuln/web/bac":
		if err := h.bacFactory.Register(reg, p.EngagementID); err != nil {
			return "", nil, fmt.Errorf("register BAC actions: %w", err)
		}
		card, err := h.skillLoader.Load(p.Skill, done_validator.IsRegistered)
		if err != nil {
			return "", nil, fmt.Errorf("load skill %s: %w", p.Skill, err)
		}
		// per-task BACValidator：绑 engagement，避免跨任务污染。
		validator := done_validator.NewBACValidator(h.engagements, h.findings, p.EngagementID)
		return card.Body, validator, nil

	default:
		return "", nil, fmt.Errorf("unknown skill: %s", p.Skill)
	}
}

// envOr 读取环境变量；空则返回 def。
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
