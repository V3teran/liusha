// Package main 是 liusha scanner 进程入口。
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入 hunter 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个 hunter agent
//	  3. healthz HTTP；graceful shutdown
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
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
	"github.com/V3teran/liusha/internal/notes"
	"github.com/V3teran/liusha/internal/observability"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools/manifest"
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
	engs := engagement.NewStore(pool)
	tasks := agentrun.NewStore(pool).WithCounter(engs)
	finds := finding.NewStore(pool).WithCounter(engs)
	calls := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	lessons := lesson.NewStore(pool)
	defer func() { _ = calls.Close() }()
	flows := flow.NewStore(pool, scannerCfg.FlowMaxRequestBody, scannerCfg.FlowMaxResponseBody).WithCounter(engs)
	creds := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	noteStore := notes.NewRedis(rdb, notes.Config{
		KeyPrefix:        cfg.Notes.RedisKeyPrefix,
		MaxEntries:       cfg.Notes.MaxEntries,
		TTL:              time.Duration(cfg.Notes.TTLHours) * time.Hour,
		CompactThreshold: cfg.Notes.CompactThreshold,
		CompactBatchSize: cfg.Notes.CompactBatchSize,
		CompactTimeout:   time.Duration(cfg.Notes.CompactTimeoutSeconds) * time.Second,
	})
	pricing := observability.NewPricing(cfg.Pricing)

	// hunter system prompt 已编译期 embed（internal/builder/hunter/system_prompt.md），
	// 不再需要运行时 skill loader 加载——下面的 vuln/tooling loader 服务 Progressive Disclosure。

	// Tooling loader（Progressive Disclosure）：root=skills/tooling，
	// 每个子目录一份 SKILL.md = 一个外部 CLI 工具的完整手册。
	// hunter buildUserPrompt 用 List() 拼"工具索引"段（Tier 1）；
	// LLM 调 read_tooling_skill(name) 拿完整 body（Tier 2）。
	// 目录不存在或扫描失败 → 置 nil，hunter 自动 fallback 不注入索引段、不注册工具。
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
	// hunter 用它渲染 SystemPrompt 的 tooling_catalog 段（Tier 1 索引）。
	// 与 SKILL.md frontmatter 解耦：删 SKILL ≠ 工具消失。
	// 路径可通过 LIUSHA_TOOLS_MANIFEST_PATH env override，缺省 deployments/tool-images/pentools/tools.yaml。
	toolsManifestPath := envOr("LIUSHA_TOOLS_MANIFEST_PATH", "deployments/tool-images/pentools/tools.yaml")
	toolsManifest, err := manifest.Load(toolsManifestPath)
	if err != nil {
		logger.Fatal().Err(err).Str("path", toolsManifestPath).Msg("tools.yaml 加载失败——LLM 看不到沙箱工具会无法 ReAct，fail-fast")
	}
	logger.Info().Strs("tools", toolsManifest.Names()).Int("count", len(toolsManifest.Tools)).Str("path", toolsManifestPath).Msg("tools manifest loaded")

	// Vuln loader（Progressive Disclosure）：root=skills/vuln，
	// 每个子目录一份 SKILL.md = 一种漏洞类型的挖掘指南。
	// hunter buildUserPrompt 用 List() 拼"漏洞挖掘指南索引"段（Tier 1）；
	// LLM 按 recon_checklist 判完方向后调 read_vuln_skill(name) 拿完整 body（Tier 2）。
	// 目录不存在或扫描失败 → 置 nil，hunter 自动 fallback 不注入索引段、不注册工具。
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
	router := llm.NewRouterWithOptions(llm.NewFactory(cfg), llm.RetryOptionsFromConfig(cfg.LLM.Retry))

	// notes Compactor：复用 reviewer 路由（light LLM，通常 Haiku），
	// 超阈值时蒸馏老 note 为 summary。失败由 noteStore 内部 fallback 到 LTRIM。
	compactorGen, err := router.For(ctx, "reviewer")
	if err != nil {
		logger.Fatal().Err(err).Msg("notes compactor: router.For(reviewer) 失败")
	}
	noteStore.WithCompactor(notes.NewLLMCompactor(compactorGen))

	// 容器化沙箱启动器（管理 sandbox 容器生命周期：per agent run 一个容器）。
	// 启动时一次性清理上次进程崩前残留的孤儿容器——max lifetime 4h + Destroy 失败兜底。
	launcher := sandbox.NewDockerLauncher(cfg.Sandbox.DefaultImage)
	if err := launcher.CleanupOrphans(ctx); err != nil {
		logger.Warn().Err(err).Msg("CleanupOrphans 失败（非致命，max lifetime 兜底）")
	}

	// hunter builder：scanner 启动时构造一次。
	// run_command 工具的 sandbox.Client 由 handlePassive/handleActive 每次 Spawn 后通过
	// skill.BuilderParams.Sandbox 注入——不持有在 Deps 里。
	hunterBuilder := hunter.NewBuilder(hunter.Deps{
		Notes:                  noteStore,
		Findings:               finds,
		Lessons:                lessons,
		Credentials:            creds,
		ToolingLoader:          toolingLoader,
		ToolsManifest:          toolsManifest,
		VulnLoader:             vulnLoader,
		SandboxCfg:             cfg.Sandbox,
		StepToolTimeoutSeconds: cfg.Toolruntime.StepToolTimeoutSeconds,
		MaxSteps:               scannerCfg.MainMaxSteps,
		WatchdogSeconds:        scannerCfg.StepLLMTimeoutSeconds,
		ReviewerEverySteps:     cfg.React.ReviewerEverySteps,
		FindingsLimit:          cfg.Engagement.FindingsLimitInPrompt,
		LessonsLimit:           cfg.Engagement.LessonsLimitInPrompt,
	})

	// handler
	h := handler{
		tasks:         tasks,
		engagements:   engs,
		notes:         noteStore,
		findings:      finds,
		lessons:       lessons,
		flows:         flows,
		calls:         calls,
		cfg:           cfg,
		scannerCfg:    scannerCfg,
		pricing:       pricing,
		router:        router,
		hunterBuilder: hunterBuilder,
		launcher:      launcher,
		logger:        logger,
	}

	mux := worker.NewMux()
	mux.Register(worker.RoleHunter, h.handle)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: scannerCfg.AsynqConcurrency,
			Queues: map[string]int{
				worker.QueueHunter: scannerCfg.QueueHunterWeight,
				worker.QueueDispatch:     scannerCfg.QueueDispatchWeight,
			},
		},
	)

	// Ingestor goroutine：消费 Redis Stream → 启发式 → 入 main 队列。
	flowCtx, flowCancel := context.WithCancel(context.Background())
	defer flowCancel()

	rotator := engagement.NewRotator(engs, engagement.RotateLimitsFromConfig(cfg.Engagement))

	trafficIngestor, err := ingestor.NewTraffic(flowCtx, ingestor.Deps{
		Redis:    rdb,
		Cfg:      cfg.Ingestor,
		Stream:   cfg.Proxy.StreamName,
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

	// Rotator sweeper goroutine：与「懒轮换」（流量进来时 EnsurePassiveSession 检查 expires_at）
	// 互补——无流量场景下也能保证「24h 一到必关」，避免 PG 堆积陈旧 active 行 + viewer 看僵尸 session。
	go func() {
		interval := time.Duration(cfg.Engagement.SweeperIntervalSeconds) * time.Second
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-flowCtx.Done():
				return
			case <-ticker.C:
				if n, err := rotator.Sweep(flowCtx); err != nil {
					logger.Warn().Err(err).Msg("engagement sweep failed")
				} else if n > 0 {
					logger.Info().Int("aborted", n).Msg("engagement sweep aborted expired session")
				}
			}
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
	notes         *notes.RedisStore // hunter 短期工作笔记板（engagement 内同 host 跨 task 共享）
	findings      *finding.Store
	lessons       *lesson.Store // reviewer LessonFetcher 用：拉该 host 历史 lesson 给 reviewer 做方向修正
	flows         *flow.Store
	calls         *llminvocation.Store
	cfg           config.Config
	scannerCfg    config.ScannerConfig
	pricing       llm.PricingProvider
	router        *llm.Router
	hunterBuilder skill.Builder
	launcher      sandbox.Launcher
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

// abortTask 把 task 推进到 aborted 终态（reviewer 终止 / engagement 中止 / ctx 取消）。
// 与 failTask 区别：aborted 是"主动收手"非错误，不应触发告警。
func (h handler) abortTask(ctx context.Context, taskID, reason string) error {
	if setErr := h.tasks.SetAborted(ctx, taskID); setErr != nil {
		h.logger.Warn().Err(setErr).Str("agent_run_id", taskID).Str("reason", reason).
			Msg("SetAborted 失败（task 留在 running）")
	}
	h.logger.Info().Str("agent_run_id", taskID).Str("reason", reason).Msg("task aborted")
	return nil
}

// handle 是单个 hunter task 的处理入口。
//
// timeout 按 mode 分档：passive 用 AgentRunTimeoutSeconds（默认 1h），
// active 用 ActiveAgentRunTimeoutSeconds（默认 4h，对齐 sandbox max lifetime）。
// 站点扫描爬+测耗时远超单条流量，独立配置避免被 passive timeout 一刀切断。
func (h handler) handle(ctx context.Context, p worker.Payload) (retErr error) {
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

	// 按 mode 选 timeout（解析 input 后才知道 mode；未知 mode 用 passive 兜底，
	// switch default 会立即报错，无超时浪费）。
	timeout := h.scannerCfg.AgentRunTimeoutSeconds
	if input.Mode == "active" {
		timeout = h.scannerCfg.ActiveAgentRunTimeoutSeconds
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
		defer cancel()
	}

	switch input.Mode {
	case "passive":
		return h.handlePassive(ctx, p, input.Entrypoint)
	case "active":
		return h.handleActive(ctx, p, input.Entrypoint)
	default:
		err := fmt.Errorf("unknown mode: %s", input.Mode)
		return h.failTask(ctx, p.TaskID, err)
	}
}

// handlePassive 处理 mode=passive 的 hunter task：拉 flow 完整 raw（请求 + 响应）
// → 装配 hunter react.Config → 跑 react.Run（1 流量 → 1 hunter agent）。
func (h handler) handlePassive(ctx context.Context, p worker.Payload, entrypoint json.RawMessage) error {
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

	// reviewer
	reviewLLMRaw, err := h.router.For(ctx, "reviewer")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	reviewLLMGen := llm.Instrument(reviewLLMRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "reviewer"},
		h.pricing,
	)
	// hostForFetchers 提前定义：reviewer 需要 host 做 notes 范围隔离。
	hostForFetchers := ep.Host
	reviewer := react.NewLLMReviewer(reviewLLMGen, h.notes, eid, hostForFetchers)
	reviewer.ArgsTruncate = h.cfg.React.ReviewerArgsTruncate
	reviewer.ObsTruncate = h.cfg.React.ReviewerObsTruncate
	// FlowSummary 约束 reviewer 只评本流量任务，避免跨流量推方向
	reviewer.FlowSummary = fmt.Sprintf("%s %s%s", ep.Method, ep.Host, ep.URL)
	// HostFindingsFetcher 让 reviewer 看到 engagement + host 范围内已有 finding 列表（背景参考）。
	// 列表仅作背景知识：reviewer 知道本 host 漏洞面，但**不**把"已有 N 条"误当成本流量任务进度——
	// 否则同 host 别的流量先挖到 finding 时，本流量（如 bac/profile 真无漏洞）会被误推
	// terminate / 编造 hint。terminate 判定完全交给 reviewer 基于 window 行为推理。
	reviewer.HostFindingsFetcher = func(ctx context.Context) ([]string, error) {
		fs, err := h.findings.ListByEngagementAndHost(ctx, eid, hostForFetchers, h.cfg.React.ReviewerFindingsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, fmt.Sprintf("[%s] %s", f.Severity, f.Summary))
		}
		return out, nil
	}
	// LessonFetcher 让 reviewer 看到该 host 历史 lesson（跨 engagement 长期经验），
	// 用于方向修正 hint。lesson 是经验，不参与"是否 terminate"决策。
	reviewer.LessonFetcher = func(ctx context.Context) ([]string, error) {
		lessons, err := h.lessons.ListByHost(ctx, hostForFetchers, h.cfg.React.ReviewerLessonsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(lessons))
		for _, l := range lessons {
			out = append(out, fmt.Sprintf("[p%d] %s", l.Priority, l.Content))
		}
		return out, nil
	}

	// 拉 flow 完整 raw（请求 + 响应）填 BuilderParams
	fl, err := h.flows.GetByID(ctx, ep.FlowID)
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("flows.GetByID(%d): %w", ep.FlowID, err))
	}

	// 为本次 agent run 启动 sandbox 容器：spawn → 等 healthz → 返回 Client。
	// defer Destroy 保证 react.Run 结束后容器被回收（正常 / 异常 / panic 路径都覆盖）；
	// 失败时由 sandbox-server max lifetime 4h + 下次 scanner 启动 CleanupOrphans 兜底。
	sandboxClient, err := h.launcher.Spawn(ctx, p.TaskID)
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("launcher.Spawn(%s): %w", p.TaskID, err))
	}
	defer func() {
		// 用独立 ctx：handle ctx 可能已被 cancel（abort 路径），仍需销毁容器
		destroyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.launcher.Destroy(destroyCtx, p.TaskID); err != nil {
			h.logger.Warn().Err(err).Str("agent_run_id", p.TaskID).
				Msg("launcher.Destroy 失败（max lifetime / 下次启动 CleanupOrphans 兜底）")
		}
	}()

	cfg, err := h.hunterBuilder(ctx, skill.BuilderParams{
		EngagementID:    eid,
		TaskID:          tid,
		FlowID:          ep.FlowID,
		Host:            ep.Host,
		URL:             ep.URL,
		Method:          ep.Method,
		LLM:             hunterGen,
		Reviewer:        reviewer,
		RequestHeaders:  fl.RequestHeaders,
		RequestBody:     fl.RequestBody,
		ResponseStatus:  fl.StatusCode,
		ResponseHeaders: fl.ResponseHeaders,
		ResponseBody:    fl.ResponseBody,
		Sandbox:         sandboxClient,
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
		// ctx 取消 / 截止视为主动 abort 非真错误
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return h.abortTask(ctx, p.TaskID, "ctx "+err.Error())
		}
		return h.failTask(ctx, p.TaskID, err)
	}
	// engagement aborted（OnAbort 触发）：走 SetAborted 而非 SetDone。
	// reviewer terminate 不再强中断（改注入 hint），所以此分支只剩 engagement-level abort。
	if out.TerminateBy == "aborted" {
		return h.abortTask(ctx, p.TaskID, out.TerminateBy)
	}

	res, err := json.Marshal(map[string]any{
		"terminate_by":     out.TerminateBy,
		"total_steps":      out.TotalSteps,
		"total_in":         out.TotalUsage.InTokens,
		"total_out":        out.TotalUsage.OutTokens,
		"total_cached":     out.TotalUsage.CachedTokens,
		"reviewer_hints":   out.ReviewerHints,
	})
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("marshal task result: %w", err))
	}
	return h.tasks.SetDone(ctx, p.TaskID, res)
}

// handleActive 处理 mode=active 的 hunter task：把 brief 自然语言整段喂 hunter，
// 由 LLM 自行从 brief 识别目标 URL / 凭据 / 测试范围。
//
// 与 handlePassive 差异：
//   - 跳过 flows.GetByID（active 无 flow）
//   - BuilderParams 走 active 字段（Brief 而非 Request*/Response*）
//   - Host 用 engagement_id 当虚拟 host（notes/findings 按 engagement 切分；
//     lesson 跨 engagement 复用对 active 价值小，trade-off 可接受）
//
// sandbox 生命周期 / reviewer 装配 / OnAbort / 终态处理完全对称 passive。
func (h handler) handleActive(ctx context.Context, p worker.Payload, entrypoint json.RawMessage) error {
	var ep struct {
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal(entrypoint, &ep); err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	if ep.Brief == "" {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("active entrypoint 缺 brief"))
	}

	tid, eid := p.TaskID, p.EngagementID
	// 虚拟 host：active 模式没有先验目标，用 eid 当 notes/findings 切分键。
	virtualHost := eid

	// hunter LLM Generator
	hunterRaw, err := h.router.For(ctx, "hunter")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	hunterGen := llm.Instrument(hunterRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "hunter"},
		h.pricing,
	)

	// reviewer
	reviewLLMRaw, err := h.router.For(ctx, "reviewer")
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}
	reviewLLMGen := llm.Instrument(reviewLLMRaw, h.calls,
		llm.CallMeta{TaskID: &tid, EngagementID: &eid, RouteKey: "reviewer"},
		h.pricing,
	)
	reviewer := react.NewLLMReviewer(reviewLLMGen, h.notes, eid, virtualHost)
	reviewer.ArgsTruncate = h.cfg.React.ReviewerArgsTruncate
	reviewer.ObsTruncate = h.cfg.React.ReviewerObsTruncate
	// FlowSummary 用 engagement 标识让 reviewer 知道是 active 站点任务
	reviewer.FlowSummary = "ACTIVE eid=" + eid
	reviewer.HostFindingsFetcher = func(ctx context.Context) ([]string, error) {
		fs, err := h.findings.ListByEngagementAndHost(ctx, eid, virtualHost, h.cfg.React.ReviewerFindingsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(fs))
		for _, f := range fs {
			out = append(out, fmt.Sprintf("[%s] %s", f.Severity, f.Summary))
		}
		return out, nil
	}
	reviewer.LessonFetcher = func(ctx context.Context) ([]string, error) {
		lessons, err := h.lessons.ListByHost(ctx, virtualHost, h.cfg.React.ReviewerLessonsLimit)
		if err != nil {
			return nil, err
		}
		out := make([]string, 0, len(lessons))
		for _, l := range lessons {
			out = append(out, fmt.Sprintf("[p%d] %s", l.Priority, l.Content))
		}
		return out, nil
	}

	// 为本次 agent run 启动 sandbox 容器（与 traffic 完全对称）。
	sandboxClient, err := h.launcher.Spawn(ctx, p.TaskID)
	if err != nil {
		return h.failTask(ctx, p.TaskID, fmt.Errorf("launcher.Spawn(%s): %w", p.TaskID, err))
	}
	defer func() {
		destroyCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := h.launcher.Destroy(destroyCtx, p.TaskID); err != nil {
			h.logger.Warn().Err(err).Str("agent_run_id", p.TaskID).
				Msg("launcher.Destroy 失败（max lifetime / 下次启动 CleanupOrphans 兜底）")
		}
	}()

	cfg, err := h.hunterBuilder(ctx, skill.BuilderParams{
		EngagementID: eid,
		TaskID:       tid,
		Host:         virtualHost,
		LLM:          hunterGen,
		Reviewer:     reviewer,
		Mode:         "active",
		Brief:        ep.Brief,
		Sandbox:      sandboxClient,
	})
	if err != nil {
		return h.failTask(ctx, p.TaskID, err)
	}

	// engagement 中止时让 react.Run 自然停（与 traffic 一致）
	cfg.OnAbort = func(c context.Context) (bool, error) {
		eng, err := h.engagements.GetByID(c, eid)
		if err != nil {
			return false, err
		}
		return eng.Status != engagement.StatusActive, nil
	}

	out, err := react.Run(ctx, cfg)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return h.abortTask(ctx, p.TaskID, "ctx "+err.Error())
		}
		return h.failTask(ctx, p.TaskID, err)
	}
	if out.TerminateBy == "aborted" {
		return h.abortTask(ctx, p.TaskID, out.TerminateBy)
	}

	res, err := json.Marshal(map[string]any{
		"terminate_by":   out.TerminateBy,
		"total_steps":    out.TotalSteps,
		"total_in":       out.TotalUsage.InTokens,
		"total_out":      out.TotalUsage.OutTokens,
		"total_cached":   out.TotalUsage.CachedTokens,
		"reviewer_hints": out.ReviewerHints,
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
