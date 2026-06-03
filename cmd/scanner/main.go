// Package main 是 liusha scanner 进程入口。
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入 hunter 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个 hunter agent
//	  3. healthz HTTP；graceful shutdown
//
// **部署约束：scanner 当前是单实例**。subtask swarm 用 in-process parentRegistries
// (sync.Map) 持有commander的 Registry + striker goroutine——commander一旦被 asynq 路由到本进程，
// 它派的所有striker也只在本进程内跑（共享 ctx 树 + sandbox 容器 + WaitAll 清理）。
// 多实例部署需先实现 Registry 跨进程协同（如 Redis-backed Registry）才能解锁。
// active commander在 enqueue 时已设 asynq.MaxRetry(0)，crash 后不重试——配合本约束
// 避免"commander 在 A 实例 crash → asynq retry 给 B → B 看不到 A 内存的 striker Registry"僵尸场景。
package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sync"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/activescan"
	"github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/grounding"
	hunterstore "github.com/V3teran/liusha/internal/hunter"
	"github.com/V3teran/liusha/internal/ingestor"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/notes"
	"github.com/V3teran/liusha/internal/observability"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/subtask"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
)

func main() {
	logger := logx.New("scanner")
	ctx := context.Background()

	cfg, err := config.Load(envx.OrDefault("LIUSHA_CONFIG", "./config/config.yaml"))
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
	passSess := passivesession.NewStore(pool) // passive session store
	actScan := activescan.NewStore(pool)      // active scan store
	tasks := hunterstore.NewStore(pool)
	finds := finding.NewStore(pool)
	toolCalls := toolinvocation.NewStore(pool)
	calls := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	lessons := lesson.NewStore(pool)
	defer func() { _ = calls.Close() }()
	flows := flow.NewStore(pool, scannerCfg.FlowMaxRequestBody, scannerCfg.FlowMaxResponseBody)
	creds := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	noteStore := notes.NewRedis(rdb, notes.Config{
		KeyPrefix:        cfg.Notes.RedisKeyPrefix,
		MaxEntries:       cfg.Notes.MaxEntries,
		TTL:              time.Duration(cfg.Notes.TTLHours) * time.Hour,
		CompactThreshold: cfg.Notes.CompactThreshold,
		CompactBatchSize: cfg.Notes.CompactBatchSize,
		CompactTimeout:   time.Duration(cfg.Notes.CompactTimeoutSeconds) * time.Second,
	}).WithLogger(logger)
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
	toolsManifestPath := envx.OrDefault("LIUSHA_TOOLS_MANIFEST_PATH", "deployments/tool-images/pentools/tools.yaml")
	toolsManifest, err := manifest.Load(toolsManifestPath)
	if err != nil {
		logger.Fatal().Err(err).Str("path", toolsManifestPath).Msg("tools.yaml 加载失败——LLM 看不到沙箱工具会无法 ReAct，fail-fast")
	}
	logger.Info().Strs("tools", toolsManifest.Names()).Int("count", len(toolsManifest.Tools)).Str("path", toolsManifestPath).Msg("tools manifest loaded")

	// Vuln loader（Progressive Disclosure）：root=skills/vuln，
	// 每个子目录一份 SKILL.md = 一种漏洞类型的挖掘指南。
	// hunter buildUserPrompt 用 List() 拼"漏洞挖掘指南索引"段（Tier 1）；
	// LLM 按 user_prompt 注入的"漏洞类型索引"判完方向后调 read_vuln_skill(name) 拿完整 body（Tier 2）。
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

	// notes Compactor：复用 inspector 路由（light LLM，通常 Haiku），
	// 超阈值时蒸馏老 note 为 summary。失败由 noteStore 内部 fallback 到 LTRIM。
	compactorGen, err := router.For(ctx, "inspector")
	if err != nil {
		logger.Fatal().Err(err).Msg("notes compactor: router.For(inspector) 失败")
	}
	noteStore.WithCompactor(notes.NewLLMCompactor(compactorGen))

	// 容器化沙箱启动器（管理 sandbox 容器生命周期：per agent run 一个容器）。
	// 启动时一次性清理上次进程崩前残留的孤儿容器——max lifetime 4h + Destroy 失败兜底。
	//
	// B1：active 容器内 browser-svc.py 内建 CDP Network observer 抓 chromium 真实流量 →
	// LIUSHA_INGEST_URL（指向 cmd/proxy healthz endpoint）→ http_flow（source=internal）。
	// CLI 工具仍直连目标不入字典；凭证共享走 redis credentials key（read/write_credential）。
	launcher := sandbox.NewDockerLauncher(cfg.Sandbox.DefaultImage)
	// 注入视口尺寸到 launcher → docker run -e → 容器内 wrapper 透传 chromium。
	launcher.ViewportWidth = cfg.Sandbox.ViewportWidth
	launcher.ViewportHeight = cfg.Sandbox.ViewportHeight
	// B1：拼 CDP capture ingest URL/token 注入 launcher → docker run -e。
	// scanner 跑在 host，容器经 host.docker.internal 回连 cmd/proxy healthz 端口（cfg.Proxy.HealthzAddr）。
	// token 与 cmd/proxy 共享同一值（ENV LIUSHA_INGEST_TOKEN 覆盖 yaml）。
	// HealthzAddr 空 / 解析失败则不注入 → browser-svc.py 读不到 LIUSHA_INGEST_URL → capture 不启用。
	if cfg.Proxy.HealthzAddr != "" {
		_, port, splitErr := net.SplitHostPort(cfg.Proxy.HealthzAddr)
		if splitErr != nil {
			logger.Warn().Err(splitErr).Str("healthz_addr", cfg.Proxy.HealthzAddr).
				Msg("解析 proxy healthz addr 失败，跳过 CDP capture ingest 注入（capture 不启用）")
		} else {
			launcher.IngestURL = "http://host.docker.internal:" + port + "/internal/v1/flows/ingest"
			launcher.IngestToken = envx.OrDefault("LIUSHA_INGEST_TOKEN", cfg.Proxy.IngestToken)
		}
	}
	if err := launcher.CleanupOrphans(ctx); err != nil {
		logger.Warn().Err(err).Msg("CleanupOrphans 失败（非致命，max lifetime 兜底）")
	}

	// hunter builder：scanner 启动时构造一次。
	// run_command 工具的 sandbox.Client 由 handlePassive/handleActive 每次 Spawn 后通过
	// skill.BuilderParams.Sandbox 注入——不持有在 Deps 里。
	//
	// 用 var + 后赋值模式装 hunterBuilder：spawnerFactory 闭包需在调用期捕获 hunterBuilder
	// 自身（spawner 装配striker时调 HunterBuilder 复用 builder 逻辑）——NewBuilder 返回值赋
	// 给 var 后，闭包在 builder 闭包真实执行时（handleActive 路径）才 deref 到已就绪的值。
	var hunterBuilder skill.Builder

	// parentRegistries：commander hunterID → striker Registry。spawnerFactory LoadOrStore；
	// handleActive 在 react.Run 返回后 LoadAndDelete + cancel + WaitAll。
	parentRegistries := &sync.Map{}

	spawnerFactory := func(parentCtx context.Context, p skill.BuilderParams) (subtask.Spawner, *subtask.Registry, error) {
		registry := subtask.NewRegistry()
		// 每commander builder 只调一次（inspector redirect 走 hint 注入不重建 builder），
		// 不存在重入路径——Store 直接覆盖即可，不加 LoadOrStore 防御（YAGNI）。
		parentRegistries.Store(p.HunterID, registry)
		spawner := subtask.NewActiveSpawner(parentCtx, subtask.ActiveSpawnerConfig{
			CommanderID:   p.HunterID,
			OwnerType:     p.OwnerType, // 与commander对齐
			OwnerID:       p.OwnerID,
			Host:          p.Host,
			AgentRuns:     tasks,
			Findings:      finds,
			Lessons:       lessons,
			Calls:         calls,
			Notes:         noteStore,
			Flows:         flows, // striker spawn 时若传 flow_id 拉 commander 流量给 striker（tracker 用，commander 通常不传）
			Router:        router,
			Pricing:       pricing,
			HunterBuilder: hunterBuilder, // 晚绑定 — handleActive 执行时已就绪
			SandboxClient: p.Sandbox,
			Registry:      registry,
			MaxChildren:   scannerCfg.MaxChildren,
			Inspector: subtask.InspectorParams{
				ArgsTruncate:  cfg.React.InspectorArgsTruncate,
				ObsTruncate:   cfg.React.InspectorObsTruncate,
				FindingsLimit: cfg.React.InspectorFindingsLimit,
				LessonsLimit:  cfg.React.InspectorLessonsLimit,
			},
			Logger: logger,
		})
		return spawner, registry, nil
	}

	// provider name → context_window 映射（hunter 装配按 p.LLM.Provider() 查表）。
	// nil 安全：runtime compactHistory 见 ctxWindow=0 自动跳过压缩。
	ctxWindows := make(map[string]int, len(cfg.Providers))
	coordSystems := make(map[string]string, len(cfg.Providers))
	for name, p := range cfg.Providers {
		if p.ContextWindow != nil {
			ctxWindows[name] = *p.ContextWindow
		}
		// 仅 vision provider 算 coord_system；非 vision provider 留空字符串
		// → browser_use click/input 装配后空 CoordSystem 走 ToRealPixels 报 err（防误用）。
		// 4 层级联推断：显式 → model name registry → vendor base_url registry → real_pixels fallback。
		if p.SupportsVision != nil && *p.SupportsVision {
			sys, source := grounding.ResolveCoordSystem(p.GroundingCoordSystem, p.DefaultModel, p.BaseURL)
			coordSystems[name] = string(sys)
			lvl := logger.Info()
			if source == "default" {
				lvl = logger.Warn()
			}
			lvl.Str("provider", name).Str("source", source).Str("coord_system", string(sys)).
				Str("model", p.DefaultModel).Msg("grounding coord_system 推断")
		}
	}
	// ReAct msgs 文本压缩器：复用 inspector 路由的 light LLM（与 notes compactor 同 Generator）。
	// failure 路径已设计为 head-truncate 兜底，不阻断 hunter 主循环。
	historyCompactor := react.NewLLMHistoryCompactor(compactorGen)

	hunterBuilder = hunter.NewBuilder(hunter.Deps{
		Notes:                  noteStore,
		Findings:               finds,
		Lessons:                lessons,
		Flows:                  flows,
		Credentials:            creds,
		ToolInvocations:        toolCalls,
		ToolingLoader:          toolingLoader,
		ToolsManifest:          toolsManifest,
		VulnLoader:             vulnLoader,
		SandboxCfg:             cfg.Sandbox,
		StepToolTimeoutSeconds: cfg.Toolruntime.StepToolTimeoutSeconds,
		PassiveMaxSteps:        scannerCfg.PassiveMaxSteps,
		ActiveMaxSteps:         scannerCfg.ActiveMaxSteps,
		WatchdogSeconds:        scannerCfg.StepLLMTimeoutSeconds,
		InspectorEverySteps:    cfg.React.InspectorEverySteps,
		MaxImagesInHistory:     cfg.React.MaxImagesInHistory,
		HistoryCompactor:       historyCompactor,
		HistoryCompact: react.HistoryCompactConfig{
			TriggerRatio:        cfg.React.HistoryCompact.TriggerRatio,
			TrailingBudgetRatio: cfg.React.HistoryCompact.TrailingBudgetRatio,
			CooldownTokenDelta:  cfg.React.HistoryCompact.CooldownTokenDelta,
		},
		HistoryCompactTimeout: time.Duration(cfg.React.HistoryCompact.CompactorTimeoutSeconds) * time.Second,
		ContextWindows:        ctxWindows,
		CoordSystems:          coordSystems,
		FindingsLimit:         cfg.Session.FindingsLimitInPrompt,
		LessonsLimit:          cfg.Session.LessonsLimitInPrompt,
		SpawnerFactory:        spawnerFactory,
	})

	// handler
	h := handler{
		tasks:            tasks,
		passiveSessions:  passSess,
		activeScans:      actScan,
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

	trafficIngestor, err := ingestor.NewTraffic(flowCtx, ingestor.Deps{
		Redis:      rdb,
		Cfg:        cfg.Ingestor,
		Stream:     cfg.Proxy.StreamName,
		Passive:    passSess,
		PassiveTTL: time.Duration(cfg.Session.MaxAgeHours) * time.Hour,
		Flows:      flows,
		Tasks:      tasks,
		Enqueuer:   wc,
		Logger:     logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("new ingestor.traffic")
	}
	go func() {
		if err := trafficIngestor.Run(flowCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("ingestor.traffic exited")
		}
	}()

	// passive_session sweeper goroutine：与「懒轮换」（流量进来时 LookupOrCreate 检查 host
	// 已有 active）互补——无流量场景下也能保证「TTL 一到必关」，避免 PG 堆积陈旧 active 行 +
	// viewer 看僵尸 session。
	go func() {
		interval := time.Duration(cfg.Session.SweeperIntervalSeconds) * time.Second
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-flowCtx.Done():
				return
			case <-ticker.C:
				if n, err := passSess.Sweep(flowCtx); err != nil {
					logger.Warn().Err(err).Msg("passive_session sweep failed")
				} else if n > 0 {
					logger.Info().Int("aborted", n).Msg("passive_session sweep aborted expired session")
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

// dualOwnerCounter 让 hunter/finding/flow.Store 的 best-effort 计数维护同时尝试
// briefHostRe 匹配 http(s):// 后到 / 或 空白 之前的 host (含端口)。
//
// 例子（捕获组 [1]）：
//
//	"测试 http://target.com:8080/login.php" → "target.com:8080"
//	"扫 https://api.foo.io/v1"             → "api.foo.io"
//	"test bar.com"                          → 无匹配（缺 http(s):// 前缀）
var briefHostRe = regexp.MustCompile(`https?://([^/\s]+)`)

// extractHostFromBrief 从 active brief 抽 URL host 当 (owner, host) 切分键。
//
// 抽不到时回退 fallback（owner_id 兜底），此时 lesson 跨 task 复用失效。
// 这是按 brief 自然语言的弱契约设计：让 active 任务能自动按真实站点身份归档
// note/finding/lesson，同时不破坏"自然语言 brief"的简单 API。
func extractHostFromBrief(brief, fallback string) string {
	m := briefHostRe.FindStringSubmatch(brief)
	if len(m) < 2 {
		return fallback
	}
	return m[1]
}
