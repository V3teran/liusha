// Package main 是 liusha agent-worker 进程入口：装配 config / pg / stores / LLM Router
// + asynq 消费者 + ReAct sniffer 角色 handler + healthz HTTP。
//
// 黑客松借鉴闭环（plan 1 part3 §借鉴增量）：
//   - LLM Router（T21.5）：按 cfg.LLM.Routes 路由 react.main / observer / distill 到不同 provider，
//     外层套 retry + fallback。
//   - Distill hook（T23.5）：finding 写库后 light_provider 蒸馏成 ≤200 字 hint，落 memory_hints。
//   - Action 中间件链（T22.5）：result_compress / loop_detect / done_validate 三层横切。
//   - Observer（T23.5）：每 5 步用 light_provider 判官，决定 keep_going / steer / abort。
//
// plan 1 仅注册 sniffer 角色 + 通用 actions（done / read_state / write_fact|idea|hint /
// write_finding / write_graph）；BAC actions（read_window / fetch_credentials 等）由 plan 2 加。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/agent/action"
	"github.com/V3teran/liusha/internal/agent/action/middleware"
	"github.com/V3teran/liusha/internal/agent/actions"
	"github.com/V3teran/liusha/internal/agent/llm"
	"github.com/V3teran/liusha/internal/agent/runtime"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/graph"
	"github.com/V3teran/liusha/internal/llmcall"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/observability"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
)

// snifferSystemPrompt 是 sniffer 角色的固定 system prompt。
//
// 工具集合明确列出 plan 1 已注册的 7 个 action；BAC 工具集合（read_window /
// fetch_credentials / replay_multi_identity / heuristic_check / compute_similarity）
// 在 plan 2 才会扩展，这里只点名让 LLM 心里有数。
const snifferSystemPrompt = `你是 sniffer 角色。每个任务对应一个 traffic_window。
你能用 read_state / write_fact / write_idea / write_hint / write_finding / write_graph / done(reason)。
plan 2 会扩展 read_window / fetch_credentials / replay_multi_identity / heuristic_check / compute_similarity 等。
done 会被系统校验，调用前请确认目标已达成（或写明 reason 表示主动跳过）。`

// resultCompressBaseDir 是 result_compress 中间件落盘的根目录。
// docker compose 中由 volume 挂在容器外，便于人类追溯大型 evidence。
const resultCompressBaseDir = "./engagement-store"

// shutdownTimeout 是 healthz HTTP 优雅关闭的超时；asynq.Shutdown 自身阻塞直到 in-flight 任务结束。
const shutdownTimeout = 5 * time.Second

func main() {
	logger := logx.New("agent-worker")
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

	tasks := task.NewStore(pool)
	engs := engagement.NewStore(pool)
	finds := finding.NewStore(pool)
	graphs := graph.NewStore(pool)
	calls := llmcall.NewStore(pool)
	skillLoader := skill.NewLoader(cfg.Skills.Root)
	_ = skillLoader // plan 2 BAC skill 加载用；plan 1 仅占位以便 vet 不报 unused

	// T21.5：LLM Router——所有 LLM 调用走 router.For(ctx, role, tools)，
	// 自动按 cfg.LLM.Routes 路由 + retry/fallback。
	router := llm.NewRouter(llm.NewFactory(cfg))

	// T23.5：Distill hook——finding 写库后异步触发，调 light_provider 蒸馏成 hint。
	// 这里 distill 不绑工具（只调 chat），tools=nil；router 内部按 "distill" 路由 + 缓存。
	distillGen, err := router.For(ctx, "distill", nil)
	if err != nil {
		logger.Fatal().Err(err).Msg("router.For(distill)")
	}
	finds.OnSaved(runtime.NewDistillHook(distillGen, engs))

	mux := worker.NewMux()
	h := snifferHandler{
		tasks:       tasks,
		engagements: engs,
		findings:    finds,
		graphs:      graphs,
		calls:       calls,
		cfg:         cfg,
		pricing:     observability.DefaultPricing,
		router:      router,
		budget: runtime.Budget{
			MaxSteps:        cfg.LLM.MaxSteps,
			MaxTokens:       50_000,
			WatchdogSeconds: 60,
		},
	}
	mux.Register(worker.RoleSniffer, h.handle)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")},
		asynq.Config{
			Concurrency: 4,
			Queues: map[string]int{
				worker.QueueSniffer:  5,
				worker.QueueOperator: 1,
			},
		},
	)

	hsMux := http.NewServeMux()
	hsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	hs := &http.Server{Addr: ":9090", Handler: hsMux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info().Str("addr", hs.Addr).Msg("agent-worker healthz listening")
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
	logger.Info().Str("signal", sig.String()).Msg("agent-worker shutting down")

	srv.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("healthz shutdown")
	}
	logger.Info().Msg("agent-worker stopped")
}

// snifferHandler 持有所有跨任务共享依赖；handle() 内每个任务建独立 Registry + Generator。
type snifferHandler struct {
	tasks       *task.Store
	engagements *engagement.Store
	findings    *finding.Store
	graphs      *graph.Store
	calls       *llmcall.Store
	cfg         config.Config
	pricing     llm.PricingProvider
	router      *llm.Router // T21.5：多 provider 路由（react.main / observer / distill）
	budget      runtime.Budget
}

// handle 是单个 sniffer 任务的处理入口：
//  1. 任务状态推进 pending → running
//  2. 注册 7 个 action + 套上 3 层中间件（result_compress / loop_detect / done_validate）
//  3. 通过 router 拿 react.main + observer Generator（observer 用 light_provider）
//  4. runtime.Run 跑 ReAct 主循环
//  5. result JSON 落库（含 observer_hints / done_force_count，用于审计）
func (h snifferHandler) handle(ctx context.Context, p worker.Payload) error {
	if err := h.tasks.SetRunning(ctx, p.TaskID); err != nil {
		return err
	}

	reg := action.NewRegistry()
	_ = reg.Register(actions.Done{})
	_ = reg.Register(&actions.ReadState{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteFact{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteIdea{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteHint{Store: h.engagements, EngagementID: p.EngagementID})
	_ = reg.Register(&actions.WriteFinding{Store: h.findings, EngagementID: p.EngagementID, TaskID: p.TaskID})
	_ = reg.Register(&actions.WriteGraph{Store: h.graphs, EngagementID: p.EngagementID})

	// T22.5：套上中间件链（result_compress / loop_detect / done_validate）。
	// DoneValidate(nil) 安全回退到 AlwaysOK；plan 2 BAC 接入 Skill 后注入真 validator。
	reg.Use(
		middleware.ResultCompress(p.EngagementID, resultCompressBaseDir),
		middleware.LoopDetect(),
		middleware.DoneValidate(nil),
	)

	// 每个 task 一个全新 Generator（tools 一次绑定，避免跨 goroutine 竞争 BindTools 内部状态）。
	mainGen, err := h.router.For(ctx, "react.main", reg.Schemas())
	if err != nil {
		_ = h.tasks.SetError(ctx, p.TaskID, err.Error())
		return err
	}

	tid, eid := p.TaskID, p.EngagementID
	gen := llm.Instrument(mainGen, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "react.main"},
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
	observer := runtime.NewLLMObserver(obsGen, h.engagements, eid)

	out, err := runtime.Run(ctx, runtime.Config{
		LLM:                gen,
		Actions:            reg,
		Budget:             h.budget,
		SystemPrompt:       snifferSystemPrompt,
		UserPrompt:         string(p.Input),
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

// envOr 读取环境变量；空则返回 def。
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
