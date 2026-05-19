// Package main 是 liusha scanner 进程入口。
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入 hunter 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个 hunter agent
//	  3. healthz HTTP；graceful shutdown
//
// **部署约束：scanner 当前是单实例**。subtask swarm 用 in-process parentRegistries
// (sync.Map) 持有父任务的 Registry + 子 goroutine——父任务一旦被 asynq 路由到本进程，
// 它派的所有子任务也只在本进程内跑（共享 ctx 树 + sandbox 容器 + WaitAll 清理）。
// 多实例部署需先实现 Registry 跨进程协同（如 Redis-backed Registry）才能解锁。
// active 父任务在 enqueue 时已设 asynq.MaxRetry(0)，crash 后不重试——配合本约束
// 避免"父在 A 实例 crash → asynq retry 给 B → B 看不到 A 内存的子 Registry"僵尸场景。
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
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
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/subtask"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
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
	//
	// 用 var + 后赋值模式装 hunterBuilder：spawnerFactory 闭包需在调用期捕获 hunterBuilder
	// 自身（spawner 装配子任务时调 HunterBuilder 复用 builder 逻辑）——NewBuilder 返回值赋
	// 给 var 后，闭包在 builder 闭包真实执行时（handleActive 路径）才 deref 到已就绪的值。
	var hunterBuilder skill.Builder

	// parentRegistries：父 taskID → 子任务 Registry。spawnerFactory LoadOrStore；
	// handleActive 在 react.Run 返回后 LoadAndDelete + cancel + WaitAll。
	parentRegistries := &sync.Map{}

	spawnerFactory := func(parentCtx context.Context, p skill.BuilderParams) (subtask.Spawner, *subtask.Registry, error) {
		registry := subtask.NewRegistry()
		// 每父任务 builder 只调一次（reviewer redirect 走 hint 注入不重建 builder），
		// 不存在重入路径——Store 直接覆盖即可，不加 LoadOrStore 防御（YAGNI）。
		parentRegistries.Store(p.TaskID, registry)
		spawner := subtask.NewActiveSpawner(parentCtx, subtask.ActiveSpawnerConfig{
			ParentTaskID:          p.TaskID,
			EngagementID:          p.EngagementID,
			Host:                  p.Host,
			AgentRuns:             tasks,
			Findings:              finds,
			Lessons:               lessons,
			Calls:                 calls,
			Notes:                 noteStore,
			Flows:                 flows, // 子 spawn 时若传 flow_id 拉父流量给子（passive 父用，active 父通常不传）
			Router:                router,
			Pricing:               pricing,
			HunterBuilder:         hunterBuilder, // 晚绑定 — handleActive 执行时已就绪
			SandboxClient:         p.Sandbox,
			Registry:              registry,
			MaxChildren:           scannerCfg.MaxChildren,
			ReviewerArgsTruncate:  cfg.React.ReviewerArgsTruncate,
			ReviewerObsTruncate:   cfg.React.ReviewerObsTruncate,
			ReviewerFindingsLimit: cfg.React.ReviewerFindingsLimit,
			ReviewerLessonsLimit:  cfg.React.ReviewerLessonsLimit,
		})
		return spawner, registry, nil
	}

	hunterBuilder = hunter.NewBuilder(hunter.Deps{
		Notes:                  noteStore,
		Findings:               finds,
		Lessons:                lessons,
		Credentials:            creds,
		ToolingLoader:          toolingLoader,
		ToolsManifest:          toolsManifest,
		VulnLoader:             vulnLoader,
		SandboxCfg:             cfg.Sandbox,
		StepToolTimeoutSeconds: cfg.Toolruntime.StepToolTimeoutSeconds,
		PassiveMaxSteps:        scannerCfg.PassiveMaxSteps,
		ActiveMaxSteps:         scannerCfg.ActiveMaxSteps,
		WatchdogSeconds:        scannerCfg.StepLLMTimeoutSeconds,
		ReviewerEverySteps:     cfg.React.ReviewerEverySteps,
		FindingsLimit:          cfg.Engagement.FindingsLimitInPrompt,
		LessonsLimit:           cfg.Engagement.LessonsLimitInPrompt,
		SpawnerFactory:         spawnerFactory,
	})

	// handler
	h := handler{
		tasks:            tasks,
		engagements:      engs,
		notes:            noteStore,
		findings:         finds,
		lessons:          lessons,
		flows:            flows,
		calls:            calls,
		cfg:              cfg,
		scannerCfg:       scannerCfg,
		pricing:          pricing,
		router:           router,
		hunterBuilder:    hunterBuilder,
		launcher:         launcher,
		logger:           logger,
		parentRegistries: parentRegistries,
	}

	mux := worker.NewMux()
	mux.Register(worker.RoleHunter, h.handle)

	srv := asynq.NewServer(
		asynq.RedisClientOpt{Addr: redisAddr},
		asynq.Config{
			Concurrency: scannerCfg.AsynqConcurrency,
			Queues: map[string]int{
				worker.QueueHunter:   scannerCfg.QueueHunterWeight,
				worker.QueueDispatch: scannerCfg.QueueDispatchWeight,
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

// handler struct + failTask/abortTask/handle 入口 已抽到 handler.go。
// handlePassive 在 handler_passive.go；handleActive 在 handler_active.go。


// envOr 读取环境变量；空则返回 def。
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// briefHostRe 匹配 http(s):// 后到 / 或 空白 之前的 host (含端口)。
//
// 例子（捕获组 [1]）：
//
//	"测试 http://target.com:8080/login.php" → "target.com:8080"
//	"扫 https://api.foo.io/v1"             → "api.foo.io"
//	"test bar.com"                          → 无匹配（缺 http(s):// 前缀）
var briefHostRe = regexp.MustCompile(`https?://([^/\s]+)`)

// extractHostFromBrief 从 active brief 抽 URL host 当 (engagement, host) 切分键。
//
// 抽不到时回退 fallback（engagement_id 兜底），此时 lesson 跨 task 复用失效。
// 这是按 brief 自然语言的弱契约设计：让 active 任务能自动按真实站点身份归档
// note/finding/lesson，同时不破坏"自然语言 brief"的简单 API。
func extractHostFromBrief(brief, fallback string) string {
	m := briefHostRe.FindStringSubmatch(brief)
	if len(m) < 2 {
		return fallback
	}
	return m[1]
}
