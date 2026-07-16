// Package main 是 liusha scanner 进程入口。
//
//	职责：
//	  1. 启 ingestor.Traffic goroutine：消费 Redis Stream → 启发式打分 → 入 hunter 队列
//	  2. 启 asynq.Server：消费 agent:react 队列，每个 task 跑 1 个 hunter agent
//	  3. healthz HTTP；graceful shutdown
//
// **部署约束：scanner 当前是单实例**。subtask swarm 用 in-process parentRegistries
// (sync.Map) 持有orchestrator的 Registry + exploitation goroutine——orchestrator一旦被 asynq 路由到本进程，
// 它派的所有exploitation也只在本进程内跑（共享 ctx 树 + sandbox 容器 + WaitAll 清理）。
// 多实例部署需先实现 Registry 跨进程协同（如 Redis-backed Registry）才能解锁。
// active orchestrator在 enqueue 时已设 asynq.MaxRetry(0)，crash 后不重试——配合本约束
// 避免"orchestrator 在 A 实例 crash → asynq retry 给 B → B 看不到 A 内存的 exploitation Registry"僵尸场景。
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
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/builder/hunter"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/einoagent"
	"github.com/V3teran/liusha/internal/einollm"
	"github.com/V3teran/liusha/internal/einotools"
	"github.com/V3teran/liusha/internal/embedding"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/finding"
	hunterstore "github.com/V3teran/liusha/internal/hunter"
	"github.com/V3teran/liusha/internal/ingestor"
	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/ratelimit"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/scenario"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
)

const (
	// scanReaperInterval：reaper 巡逻周期。两条 (status, heartbeat_at) 索引 UPDATE，常态命中 0 行，
	// 30s 既灵敏又不扰库。
	scanReaperInterval = 30 * time.Second
	// scanReaperStaleBuffer：判死阈值在「最长合法工具+LLM 间隔」之上再加的安全缓冲（时钟偏移 / 调度抖动）。
	scanReaperStaleBuffer = 2 * time.Minute
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
	taskStore := task.NewStore(pool)               // 统一 task store（合并 active_scan + passive_session）
	assignmentStore := assignment.NewStore(pool)   // 聚合建 passive assignment（一切 task 皆属某 assignment）
	convStore := conversation.NewStore(pool)       // 对话/消息 store（阶段B 过程事件落库）
	eventPublisher := scanstream.NewPublisher(rdb) // 过程事件实时广播（阶段B redis 管道）
	hunters := hunterstore.NewStore(pool)
	finds := finding.NewStore(pool)
	toolCalls := toolinvocation.NewStore(pool)
	calls := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	corpusStore := corpus.NewStore(pool) // 跨目标知识库（hybrid RAG）
	defer func() { _ = calls.Close() }()
	proxyFlows := traffic.NewProxyStore(pool) // 代理捕获流量（passive，按 host）
	agentFlows := traffic.NewAgentStore(pool) // agent 自产流量（active，按 task）
	creds := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	// 情报黑板（§7），与 credential 同 Redis 租户命名空间；ttl 滚动过期（每次写刷新该 host TTL）。
	leads := lead.NewStore(rdb, cfg.Credential.RedisKeyPrefix, time.Duration(cfg.Scanner.LeadTTLHours)*time.Hour)

	// Jina embedding + rerank client（corpus hybrid RAG 用）。密钥走 ENV JINA_API_KEY；
	// 缺失时 jinaClient=nil，corpus 降级（search 退纯 sparse、write 不 embed）——不阻塞渗透主流程。
	var embedder einotools.CorpusEmbedder
	var reranker corpus.Reranker
	if jc, err := embedding.NewClient(os.Getenv("JINA_API_KEY")); err != nil {
		logger.Warn().Err(err).Msg("JINA_API_KEY 未配置，corpus 降级为纯 sparse 检索（不影响主流程）")
	} else {
		embedder = jc
		reranker = jc
	}

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
	// 容器化沙箱启动器（管理 sandbox 容器生命周期：per agent run 一个容器）。
	// 启动时一次性清理上次进程崩前残留的孤儿容器——max lifetime 4h + Destroy 失败兜底。
	//
	// active 容器内抓流量 → agent_traffic（source=internal）：
	//   - 浏览器：browser-svc.py 内建 CDP Network observer 抓 chromium 真实流量
	//   - CLI：容器内本地 mitmproxy + mitm-capture.py，工具经 HTTP_PROXY 走它
	// 两者都经 LIUSHA_INGEST_URL POST 到 scanner 自己的 ingest endpoint（见下方 hsMux 注册）。
	// 凭证共享走 redis credentials key（read/write_credential）。
	launcher := sandbox.NewDockerLauncher(cfg.Sandbox.DefaultImage)
	// 注入视口尺寸到 launcher → docker run -e → 容器内 wrapper 透传 chromium。
	launcher.ViewportWidth = cfg.Sandbox.ViewportWidth
	launcher.ViewportHeight = cfg.Sandbox.ViewportHeight
	// 拼 ingest URL/token 注入 launcher → docker run -e。scanner 跑在 host，容器经
	// host.docker.internal 回连 scanner 自己的 healthz 端口（cfg.Scanner.HealthzAddr，默认 :9090）。
	// token 与本进程 ingest handler 共享同一值（ENV LIUSHA_INGEST_TOKEN 覆盖 yaml）。
	// 解析失败则不注入 → 沙箱读不到 LIUSHA_INGEST_URL → capture 不启用。
	// 解析失败则不注入 → browser-svc.py/mitm-capture.py 读不到 LIUSHA_INGEST_URL，capture 不启用。
	if _, port, splitErr := net.SplitHostPort(scannerCfg.HealthzAddr); splitErr != nil {
		logger.Warn().Err(splitErr).Str("healthz_addr", scannerCfg.HealthzAddr).
			Msg("解析 scanner healthz addr 失败，跳过 ingest 注入（capture 不启用）")
	} else {
		launcher.IngestURL = "http://host.docker.internal:" + port + "/internal/v1/flows/ingest"
		launcher.IngestToken = envx.OrDefault("LIUSHA_INGEST_TOKEN", cfg.Proxy.IngestToken)
	}
	if err := launcher.CleanupOrphans(ctx); err != nil {
		logger.Warn().Err(err).Msg("CleanupOrphans 失败（非致命，max lifetime 兜底）")
	}

	// hunter builder：scanner 启动时构造一次。
	// hunterDeps：prompt 拼装 + eino 工具装配的共享依赖（run_command 的 sandbox.Client 由
	// handler 每次 Spawn 注入，不持有在 Deps）。react 退路已删，只剩 eino 用的 store/loader/manifest。
	hunterDeps := hunter.Deps{
		Findings:        finds,
		Credentials:     creds,
		Lead:            leads,
		ToolInvocations: toolCalls,
		ToolingLoader:   toolingLoader,
		ToolsManifest:   toolsManifest,
		VulnLoader:      vulnLoader,
		FindingsLimit:   cfg.Session.FindingsLimitInPrompt,
	}

	// active deep 角色加载（hunters/active/*.md）：deep 装配主代理 + 杀伤链子代理。
	// 解析失败 / 无 orchestrator → fail-fast（active 扫描会无法装配 deep）。
	activeDir := filepath.Join(cfg.Hunters.Root, "active")
	roles, err := einoagent.LoadRoles(activeDir)
	if err != nil {
		logger.Fatal().Err(err).Str("dir", activeDir).Msg("active 角色加载失败——active 走 deep 需 hunters/active/*.md，fail-fast")
	} else {
		roleIDs := make([]string, 0, len(roles))
		for _, r := range roles {
			roleIDs = append(roleIDs, string(r.Kind)+":"+r.ID)
		}
		logger.Info().Strs("roles", roleIDs).Str("dir", activeDir).Msg("active deep 角色加载完成")
	}

	// passive 角色加载（hunters/passive/traffic-analysis.md）：passive 单 agent 用其 prompt + max_iterations。
	// 子目录隔离 active/passive——active 的 LoadRoles 不会扫到 passive，passive 角色也不会被 deep swarm 误派。
	passiveDir := filepath.Join(cfg.Hunters.Root, "passive")
	passiveRoles, perr := einoagent.LoadRoles(passiveDir)
	if perr != nil {
		logger.Fatal().Err(perr).Str("dir", passiveDir).Msg("passive 角色加载失败——passive 需 hunters/passive/traffic-analysis.md，fail-fast")
	}
	var passiveRole einoagent.RoleDef
	for _, r := range passiveRoles {
		if r.ID == "traffic-analysis" {
			passiveRole = r
			break
		}
	}
	if passiveRole.ID == "" {
		logger.Fatal().Str("dir", passiveDir).Msg("passive 角色缺 traffic-analysis，fail-fast")
	}

	// 场景 role 加载（scenarios/*.md，阶段C）：active/passive handler 按 Payload.ScenarioID 注入主代理人设。
	// 加载失败仅警告——不注入人设退化为通用扫描，不阻塞 scanner。
	scenarioRoles, err := scenario.LoadRoles(envx.OrDefault("LIUSHA_ROLES_DIR", "./scenarios"))
	if err != nil {
		logger.Warn().Err(err).Msg("场景 role 加载失败（不注入人设，退化通用扫描）")
	} else {
		ids := make([]string, 0, len(scenarioRoles))
		for _, r := range scenarioRoles {
			ids = append(ids, string(r.Mode)+":"+r.ID)
		}
		logger.Info().Strs("scenario_roles", ids).Msg("场景 role 加载完成")
	}

	// handler
	// per-host 并发信号量（§4.3）。TTL = active 超时 + 10min 缓冲：防长 task 运行期计数键被
	// TTL 误清导致 host 额度漂移；持有者崩溃时靠 TTL 到期兜底清零，不永久泄漏。
	hostSemTTL := time.Duration(scannerCfg.ActiveAgentRunTimeoutSeconds)*time.Second + 10*time.Minute
	hostSem := ratelimit.NewHostSemaphore(rdb, cfg.Credential.RedisKeyPrefix, scannerCfg.PerHostConcurrency, hostSemTTL)

	h := handler{
		hunters:        hunters,
		tasks:          taskStore,
		findings:       finds,
		corpus:         corpusStore,
		embedder:       embedder,
		reranker:       reranker,
		leads:          leads,
		proxyFlows:     proxyFlows,
		agentFlows:     agentFlows,
		calls:          calls,
		hostSem:        hostSem,
		cfg:            cfg,
		scannerCfg:     scannerCfg,
		launcher:       launcher,
		logger:         logger,
		einoFactory:    einollm.New(cfg),
		hunterDeps:     hunterDeps,
		roles:          roles,
		passiveRole:    passiveRole,
		conversations:  convStore,
		eventPublisher: eventPublisher,
		scenarioRoles:  scenarioRoles,
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
		Redis:         rdb,
		Cfg:           cfg.Ingestor,
		Stream:        cfg.Proxy.StreamName,
		Tenant:        cfg.Credential.RedisKeyPrefix,
		Assignments:   assignmentStore,
		Tasks:         taskStore,
		ProxyFlows:    proxyFlows,
		AgentFlows:    agentFlows,
		Hunters:       hunters,
		Conversations: convStore, // passive 聚合建 task 后建对话流
		Enqueuer:      wc,
		Logger:        logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("new ingestor.traffic")
	}
	go func() {
		if err := trafficIngestor.Run(flowCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("ingestor.traffic exited")
		}
	}()

	// task reaper goroutine（B2 进度探活）：task.heartbeat_at 由 agent 每次工具调用驱动续命
	// （见 einoToolSink.heartbeat）+ handler 入口重置一次。scanner 进程崩溃或扫描卡死后心跳停摆，
	// reaper 据此把超时孤儿判为 aborted——否则前端永远显示「进行中」。
	//
	// 合表后 passive_session 的 TTL sweeper 已删（passive task 是有界批分析，跑完即终态，无常驻监控
	// 会话概念，无 TTL 轮换）。reaper 按 mode 分别判活：active run 内可能跑长工具（sqlmap/nmap）+
	// 慢 LLM，staleAfter 较长；passive 单批分析轻量，用同一 staleAfter 亦安全（偏保守不冤杀）。
	//
	// staleAfter 必须 > 单 run 内两次工具调用之间的最长合法间隔，否则冤杀正在干活的扫描：
	// 最长间隔 ≈ 一次长工具执行(step_tool_timeout) + 决定下一步的 LLM 生成(step_llm_timeout) +
	// 可能的上下文压缩 LLM(step_llm_timeout) + 缓冲。据此动态推导，不写死。
	go func() {
		staleAfter := time.Duration(cfg.Toolruntime.StepToolTimeoutSeconds+2*cfg.Scanner.StepLLMTimeoutSeconds)*time.Second + scanReaperStaleBuffer
		logger.Info().Dur("stale_after", staleAfter).Dur("interval", scanReaperInterval).Msg("task reaper started")
		ticker := time.NewTicker(scanReaperInterval)
		defer ticker.Stop()
		for {
			select {
			case <-flowCtx.Done():
				return
			case <-ticker.C:
				for _, mode := range []task.Mode{task.ModeActive, task.ModePassive} {
					if n, err := taskStore.ReapStale(flowCtx, mode, staleAfter); err != nil {
						logger.Warn().Err(err).Str("mode", string(mode)).Msg("task reap stale failed")
					} else if n > 0 {
						logger.Warn().Int("aborted", n).Str("mode", string(mode)).Dur("stale_after", staleAfter).
							Msg("task 心跳超时回收（进程崩溃或扫描卡死）")
					}
				}
			}
		}
	}()

	// healthz HTTP + active 抓流量 ingest 端点（从 cmd/proxy 迁来）。
	// 沙箱内 CLI(本地 mitmproxy)/浏览器(CDP) 抓的流量 POST 到这里 → trafficIngestor.SubmitInternal
	// 直接入进程内队列 → drain goroutine 落 agent_traffic。同进程直送，不再绕 redis（internal 自环冗余）。
	hsMux := http.NewServeMux()
	hsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	hsMux.HandleFunc("/internal/v1/flows/ingest",
		newIngestHandler(trafficIngestor, envx.OrDefault("LIUSHA_INGEST_TOKEN", cfg.Proxy.IngestToken), logger))
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
// 抽不到时回退 fallback（task_id 兜底），此时 lesson 跨 task 复用失效。
// 这是按 brief 自然语言的弱契约设计：让 active 任务能自动按真实站点身份归档
// note/finding/lesson，同时不破坏"自然语言 brief"的简单 API。
func extractHostFromBrief(brief, fallback string) string {
	m := briefHostRe.FindStringSubmatch(brief)
	if len(m) < 2 {
		return fallback
	}
	return m[1]
}
