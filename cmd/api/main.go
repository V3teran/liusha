// Package main 是 liusha api 进程入口：装配 config / pg / redis / stores / httpapi。
//
// 启动顺序：logger → config（含 ENV 覆盖）→ pg+redis → stores → http server → 监听 SIGINT/SIGTERM。
// 关闭顺序：收到信号后用 5s 超时 ctx 调 srv.Shutdown，再让 defer 关 pool/redis。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/attackgraph"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/hunter"
	"github.com/V3teran/liusha/internal/intent"
	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/qa"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/scenario"
	"github.com/V3teran/liusha/internal/sitemap"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// auditLog 专记审计写入的 best-effort 失败——审计是安全/合规轨迹，
// 失败不阻塞业务但必须留可见日志（不能完全静默吞掉）。
var auditLog = logx.New("api.audit")

func main() {
	logger := logx.New("api")
	ctx := context.Background()

	cfg, err := config.Load(envx.OrDefault("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}

	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"),
		cfg.Postgres.MaxConns, cfg.Postgres.MinConns,
		cfg.Postgres.ConnectTimeoutSeconds, cfg.Postgres.MaxConnLifetimeSeconds)
	if err != nil {
		logger.Fatal().Err(err).Msg("pg")
	}
	defer pool.Close()

	rdb, err := db.NewRedis(ctx, os.Getenv("LIUSHA_REDIS_ADDR"), cfg.Redis)
	if err != nil {
		logger.Fatal().Err(err).Msg("redis")
	}
	defer func() { _ = rdb.Close() }()

	credAPI := credential.NewRedis(rdb, cfg.Credential.RedisKeyPrefix)
	taskStore := task.NewStore(pool)
	assignmentStore := assignment.NewStore(pool)
	cronStore := cronschedule.NewStore(pool) // 定时模板（§3.3/§4.2），Scheduler goroutine 轮询
	findStore := finding.NewStore(pool)
	agentFlowStore := traffic.NewAgentStore(pool) // sitemap 攻击面从 agent_traffic 派生
	proxyFlowStore := traffic.NewProxyStore(pool) // cron 定时触发 passive 展开时领取该 host 未消费流量
	projector := &sitemap.Projector{
		Findings: findStore,
		Flows:    agentFlowStore, // 攻击面从 agent_traffic 派生（按 task）
		Tasks:    taskStore,      // sitemap 仅 active 模式（passive 用 findings 列表）
	}
	invocationStore := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	defer func() { _ = invocationStore.Close() }()

	// hunter run store + asynq 入队器。
	hunterStore := hunter.NewStore(pool)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")})
	defer enq.Close()
	auditStore := audit.NewStore(pool)         // 0047：task abort / create 审计
	convStore := conversation.NewStore(pool)   // 阶段B：会话/消息
	toolStore := toolinvocation.NewStore(pool) // 会话用量合计：工具耗时来源
	// 执行图（思维链+成果链）read-model 投影：复用 conv/finding store，不落表（docs/attack-graph-design.md）。
	attackGraphProjector := &attackgraph.Projector{Messages: convStore, Findings: findStore, Conv: convStore}
	// 阶段C：场景 role（scenarios/*.md）。加载失败仅警告——/roles 返回空、/chat 用空 role 兜底，
	// 不阻塞 api 启动（场景人设是增强，缺了退化为通用扫描）。
	scenarioRoles, err := scenario.LoadRoles(envx.OrDefault("LIUSHA_ROLES_DIR", "./scenarios"))
	if err != nil {
		logger.Warn().Err(err).Msg("场景 role 加载失败（/roles 返回空，/chat 用空 role）")
	} else {
		ids := make([]string, 0, len(scenarioRoles))
		for _, r := range scenarioRoles {
			ids = append(ids, string(r.Mode)+":"+r.ID)
		}
		logger.Info().Strs("scenario_roles", ids).Msg("场景 role 加载完成")
	}
	// 多轮问答/意图分类依赖：light provider 路由 + 问答读 finding + SSE publish。
	router := llm.NewRouterWithOptions(llm.NewFactory(cfg), llm.RetryOptionsFromConfig(cfg.LLM.Retry))
	// 执行图里程碑摘要：用 light LLM 把子代理推理总结成一句（派生层，按需调用）。
	attackGraphProjector.Summary = llmSummarizer{router: router}
	publisher := scanstream.NewPublisher(rdb)
	activeAdapter := &activeScanAdapter{assignments: assignmentStore, tasks: taskStore, hunters: hunterStore, enq: enq, audit: auditStore, conversations: convStore, roles: scenarioRoles, router: router, findings: findStore, publisher: publisher, activeRunTimeout: time.Duration(cfg.Scanner.ActiveAgentRunTimeoutSeconds) * time.Second}

	// cron Scheduler（§4.2/§10 P4）：轮询 cron_schedule 到点模板 → 克隆 assignment → 展开 task。
	// 单副本够用；ctx 随进程关停取消（无需独立 shutdown 时限——轮询循环立即退出，无 in-flight 状态要收尾）。
	cronCtx, cronCancel := context.WithCancel(context.Background())
	defer cronCancel()
	runner := &cronRunner{
		schedules:   cronStore,
		assignments: assignmentStore,
		tasks:       taskStore,
		proxyFlows:  proxyFlowStore,
		hunters:     hunterStore,
		enq:         enq,
		active:      activeAdapter,
		logger:      logger,
	}
	go runner.run(cronCtx)

	// SSE stream cookie 密钥：会话功能开启时必填（EventSource 鉴权用），缺失 fail-fast。
	streamSecret := []byte(os.Getenv("LIUSHA_STREAM_COOKIE_SECRET"))
	if len(streamSecret) == 0 {
		logger.Fatal().Msg("LIUSHA_STREAM_COOKIE_SECRET 未配置——SSE stream cookie 鉴权需要它（fail-fast）")
	}

	// 监听地址：优先 ENV（运维临时切换）→ yaml。
	listenAddr := envx.OrDefault("LIUSHA_API_ADDR", cfg.API.ListenAddr)
	srv := &http.Server{
		Addr: listenAddr,
		Handler: httpapi.NewServer(httpapi.Deps{
			APIKey:             os.Getenv("LIUSHA_API_KEY"),
			StreamCookieSecret: streamSecret,
			CookieSecure:       os.Getenv("LIUSHA_COOKIE_SECURE") == "true",
			Credentials:        credAPI,
			Tasks: taskAPIAdapter{
				tasks: taskStore,
				audit: auditStore,
			},
			Sitemap:           projector,
			Findings:          findStore,            // 全局漏洞台账（active+passive 全量 + triage 处置）
			AttackGraph:       attackGraphProjector, // 执行图（思维链+成果链）投影
			Invocations:       invocationStore,
			ActiveScan:        activeAdapter,
			Chat:              activeAdapter,                // 阶段B：POST /chat 会话发起扫描
			FollowUp:          activeAdapter,                // 多轮：POST /conversations/:id/messages 动作续接
			Abort:             activeAdapter,                // 多轮：POST /conversations/:id/abort 停止会话关联扫描
			Deleter:           activeAdapter,                // DELETE /conversations/:id 删会话+消息；关联扫描进行中拒删（409，先停后删）
			Renamer:           convStore,                    // PATCH /conversations/:id 重命名标题（convStore.SetTitle 直接满足）
			Conversations:     convStore,                    // 阶段B：会话列表 / 消息回看
			EventStream:       eventStreamAdapter{rdb: rdb}, // 阶段B：SSE 订阅 redis 事件
			Roles:             activeAdapter,                // 阶段C：GET /roles 场景列表
			UsageTasks:        convStore,                    // 会话用量：会话→task 解析
			UsageLLM:          invocationStore,              // 会话用量：LLM token/耗时合计
			UsageTools:        toolStore,                    // 会话用量：工具耗时合计
			EnableDevAutofill: envx.OrDefault("LIUSHA_DEV_AUTOFILL", "") != "",
		}),
		ReadTimeout:  time.Duration(cfg.API.ReadTimeoutSeconds) * time.Second,
		WriteTimeout: time.Duration(cfg.API.WriteTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info().Str("addr", srv.Addr).Msg("api listening")
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal().Err(err).Msg("api serve")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	logger.Info().Str("signal", sig.String()).Msg("api shutting down")

	shutdownTimeout := time.Duration(cfg.API.ShutdownTimeoutSeconds) * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("api shutdown")
	}
	logger.Info().Msg("api stopped")
}

// taskAPIAdapter 把 task store 适配到 httpapi.TaskAPI 窄接口。
//
// Abort/List 直接走 task store，无双表试探。
// HTTP API 不暴露 errMsg：用户主动取消 task 即视为正常结束，abort 恒传 ""。
type taskAPIAdapter struct {
	tasks *task.Store
	audit *audit.Store // 0047：abort 写审计事件；nil 时跳过
}

// Abort 把 task 置为 aborted。errMsg 恒空（用户主动 abort 视为正常结束）。
// 0047：成功 abort 后写 audit_log（actor=api_user，target_kind=task）。
func (a taskAPIAdapter) Abort(ctx context.Context, id string) error {
	if _, err := a.tasks.GetByID(ctx, id); err != nil {
		return fmt.Errorf("task %s not found: %w", id, err)
	}
	if err := a.tasks.Abort(ctx, id, ""); err != nil {
		return err
	}
	a.writeAudit(ctx, audit.ActionTaskAbort, "task", id)
	return nil
}

// writeAudit best-effort 写审计事件；失败不阻塞业务，但记 Warn 留可见痕迹。
func (a taskAPIAdapter) writeAudit(ctx context.Context, action, kind, id string) {
	if a.audit == nil {
		return
	}
	if _, err := a.audit.Append(ctx, audit.Event{
		Actor:      audit.ActorAPIUser,
		Action:     action,
		TargetKind: kind,
		TargetID:   id,
	}); err != nil {
		auditLog.Warn().Err(err).Str("action", action).Str("target", id).Msg("审计事件写入失败（不阻塞业务）")
	}
}

// List 列出最近的 task → httpapi.TaskSummary（前端下拉/列表）。mode 混列，按 created_at desc。
func (a taskAPIAdapter) List(ctx context.Context, limit int) ([]httpapi.TaskSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	tasks, err := a.tasks.List(ctx, "", limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	out := make([]httpapi.TaskSummary, 0, len(tasks))
	for _, t := range tasks {
		var scope map[string]string
		if t.Mode == task.ModePassive {
			scope = map[string]string{"host": t.TargetHost}
		} else {
			scope = map[string]string{"brief": t.Brief}
		}
		scopeJSON, _ := json.Marshal(scope)
		s := httpapi.TaskSummary{
			ID:           t.ID,
			Scope:        string(scopeJSON),
			Status:       string(t.Status),
			Mode:         string(t.Mode),
			CreatedAt:    t.CreatedAt.Format(time.RFC3339),
			ErrorMessage: t.ErrorMessage,
		}
		if t.EndedAt != nil {
			s.EndedAt = t.EndedAt.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	return out, nil
}

// activeScanAdapter 把 task store + hunter.Store + worker.Client 组合成
// httpapi.ActiveScanAPI 一站式入口：建 active scan → 建 hunter agent_run → 入 asynq 队列。
//
// 任一步失败都不留中间状态（前面失败直接返错；task 已建但 enqueue 失败会留
// active scan，由用户手动 abort 或后续 sweeper——保持简单不上事务，与
// passive 模式 ingestor.enqueueMain 一致语义）。
// 合表后：task.Store 管扫描生命周期，hunter.Store 建 run。
type activeScanAdapter struct {
	assignments   *assignment.Store
	tasks         *task.Store
	hunters       *hunter.Store
	enq           *worker.Client
	audit         *audit.Store        // 0047：create 写审计事件；nil 跳过
	conversations *conversation.Store // 阶段B：StartChatScan 建会话；nil 时仅 CreateActiveScan 可用
	roles         []scenario.Role     // 阶段C：场景 role（StartChatScan 默认兜底 + ListRoles 暴露）

	router    *llm.Router           // 多轮：意图分类 + 问答（light provider）
	findings  *finding.Store        // 问答读 task 黑板 finding
	publisher *scanstream.Publisher // 问答回答 publish SSE

	// active run 整体超时（= scanner.ActiveAgentRunTimeoutSeconds）。入队时设为 asynq.Timeout，
	// 否则 asynq 默认 30min 任务 deadline 会架空 scanner handler 里 4h 的 WithTimeout——
	// run 跑到 30min 就被 ctx cancel（实测 active 扫描 30min 整 abort、orchestrator 没机会收尾）。
	activeRunTimeout time.Duration
}

// ListRoles 满足 httpapi.RolesAPI：列出所有场景 role 供前端选择。
func (a *activeScanAdapter) ListRoles() []scenario.Role { return a.roles }

// defaultActiveRoleID 返回默认 active 场景 id（用户未选 role 时兜底）；无 active role 时返空。
func (a *activeScanAdapter) defaultActiveRoleID() string {
	if r, ok := scenario.DefaultForMode(a.roles, scenario.ModeActive); ok {
		return r.ID
	}
	return ""
}

// eventStreamAdapter 把 scanstream 订阅适配成 httpapi.EventStream（SSE handler 用）。
// scanstream.Subscription 自带 Events()/Close()，满足 httpapi.EventSubscription。
type eventStreamAdapter struct{ rdb *redis.Client }

func (e eventStreamAdapter) Subscribe(ctx context.Context, conversationID string) httpapi.EventSubscription {
	return scanstream.Subscribe(ctx, e.rdb, conversationID)
}

// createScan 是建 active scan 的核心：建 assignment + task + hunter run + 入 asynq 队列（带
// conversationID）。CreateActiveScan（无会话纯后台）与 StartChatScan（会话发起）共用。
func (a *activeScanAdapter) createScan(ctx context.Context, brief, conversationID, scenarioID string) (string, string, error) {
	// 一切下发皆走 assignment（§3.1）：单发 = 单元素 assignment(active, manual) → 1 task。
	asg, err := a.assignments.Create(ctx, assignment.NewParams{
		Mode:   assignment.ModeActive,
		Source: assignment.SourceManual,
		Items:  []assignment.Item{{Brief: brief}},
		Title:  briefTitle(brief),
	})
	if err != nil {
		return "", "", fmt.Errorf("create assignment: %w", err)
	}
	return a.expandActiveItem(ctx, asg.ID, brief, conversationID, scenarioID)
}

// expandActiveItem 把 assignment 下的一个 active item（brief）展开成 task + hunter run + enqueue。
// 单发（createScan，建单元素 assignment 后展开 1 条）与批量/定时（cron Scheduler，建多元素
// assignment 后逐条展开）复用同一份展开逻辑，只是 assignment 的建法不同（§3.1 单发 vs 批量/cron）。
//
// target_host 留空——暂不在 API 层 parse brief，hunter LLM 从 brief 自识别（scanner 入口回填）。
func (a *activeScanAdapter) expandActiveItem(ctx context.Context, assignmentID, brief, conversationID, scenarioID string) (string, string, error) {
	// scope 与 entrypoint 都只装 brief 原文——目标 URL / host 由 hunter LLM
	// 从 brief 自然语言里自行识别（不在 API 层做 NL parser）。
	body, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", "", fmt.Errorf("marshal brief: %w", err)
	}
	tk, err := a.tasks.Create(ctx, task.NewParams{Mode: task.ModeActive, AssignmentID: assignmentID, Brief: brief})
	if err != nil {
		return "", "", fmt.Errorf("create task: %w", err)
	}
	payloadInput, err := json.Marshal(map[string]any{
		"mode":       "active",
		"entrypoint": json.RawMessage(body),
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}

	tid, err := a.hunters.Create(ctx, hunter.NewParams{
		TaskID: tk.ID,
		Role:   "orchestrator",
		Input:  payloadInput,
	})
	if err != nil {
		return "", "", fmt.Errorf("create hunter run: %w", err)
	}

	// active orchestrator跑 ~4h，asynq 默认 retry 25 次 → 4 天死循环；且 retry 接管时
	// 新 scanner 进程 parentRegistries 是空的，PreDoneCheck 永放行，旧 PG exploitation 留
	// status=running 僵尸态 + 前端看到"orchestrator done + exploitation running"矛盾。
	// MaxRetry(0)：orchestrator跑挂就跑挂，让用户手动 abort + 重新触发，不重试。
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       tid,
		TaskID:         tk.ID,
		ConversationID: conversationID, // 阶段B：会话发起时非空 → scanner 发过程事件
		ScenarioID:     scenarioID,     // 阶段C：场景 role → scanner 注入主代理人设
		Input:          payloadInput,
		Role:           worker.RoleHunter,
	}, asynq.MaxRetry(0), asynq.Timeout(a.activeRunTimeout)); err != nil {
		return "", "", fmt.Errorf("enqueue: %w", err)
	}

	// 0047：task 创建成功 → 审计事件。metadata 记 brief 前 200 字便于事后查（完整 brief 在 task.brief 列）。
	if a.audit != nil {
		briefPreview := brief
		if len(briefPreview) > 200 {
			briefPreview = briefPreview[:200]
		}
		meta, _ := json.Marshal(map[string]string{"brief_preview": briefPreview, "hunter_id": tid})
		if _, err := a.audit.Append(ctx, audit.Event{
			Actor:      audit.ActorAPIUser,
			Action:     audit.ActionTaskCreate,
			TargetKind: "task",
			TargetID:   tk.ID,
			Metadata:   meta,
		}); err != nil {
			// best-effort：审计失败不阻塞业务返回，但记 Warn 留可见痕迹
			auditLog.Warn().Err(err).Str("action", string(audit.ActionTaskCreate)).Str("target", tk.ID).Msg("task 创建审计写入失败（不阻塞业务）")
		}
	}

	return tk.ID, tid, nil
}

// FollowUpScan 在已有 task 上发起一次续接 run（多轮动作）：重开 task + 建 hunter run +
// 入队（brief=追加消息）。复用 task 作用域黑板——新 orchestrator 经 BuildUserPrompt 看到先前 finding。
// 入队 Payload 与 createScan 同构，仅 TaskID 复用传入 taskID、不新建 task。
func (a *activeScanAdapter) FollowUpScan(ctx context.Context, taskID, conversationID, scenarioID, brief string) (string, error) {
	if err := a.tasks.Reopen(ctx, taskID); err != nil {
		return "", fmt.Errorf("reopen task: %w", err)
	}
	body, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", fmt.Errorf("marshal brief: %w", err)
	}
	payloadInput, err := json.Marshal(map[string]any{"mode": "active", "entrypoint": json.RawMessage(body)})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	tid, err := a.hunters.Create(ctx, hunter.NewParams{
		TaskID: taskID,
		Role:   "orchestrator",
		Input:  payloadInput,
	})
	if err != nil {
		return "", fmt.Errorf("create hunter run: %w", err)
	}
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       tid,
		TaskID:         taskID,
		ConversationID: conversationID,
		ScenarioID:     scenarioID,
		Input:          payloadInput,
		Role:           worker.RoleHunter,
	}, asynq.MaxRetry(0), asynq.Timeout(a.activeRunTimeout)); err != nil {
		return "", fmt.Errorf("enqueue followup: %w", err)
	}
	return tid, nil
}

// FollowUpPassive 是 passive 会话内的 action 续接：Reopen 原 task（沿用同一 task 累积 finding）
// → 起一个 traffic-analysis run，entrypoint 带原 host + 用户手敲指令（directive）。
// 与 FollowUpScan 同构，差异：mode=passive、role=traffic-analysis、directive 透传给 agent 验证。
//
// directive 是用户在会话里手敲的内容（自然语言指导，或直接粘的请求/命令）——passive handler
// 拼进 prompt，让 agent 用 run_command/replay_traffic 照打验证；原批流量仍全读，上下文不丢。
func (a *activeScanAdapter) FollowUpPassive(ctx context.Context, taskID, conversationID, host, directive string) (string, error) {
	if err := a.tasks.Reopen(ctx, taskID); err != nil {
		return "", fmt.Errorf("reopen task: %w", err)
	}
	body, err := json.Marshal(map[string]string{"host": host, "directive": directive})
	if err != nil {
		return "", fmt.Errorf("marshal entrypoint: %w", err)
	}
	payloadInput, err := json.Marshal(map[string]any{"mode": "passive", "entrypoint": json.RawMessage(body)})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	tid, err := a.hunters.Create(ctx, hunter.NewParams{
		TaskID: taskID,
		Role:   "traffic-analysis",
		Input:  payloadInput,
	})
	if err != nil {
		return "", fmt.Errorf("create hunter run: %w", err)
	}
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       tid,
		TaskID:         taskID,
		ConversationID: conversationID,
		Input:          payloadInput,
		Role:           worker.RoleHunter,
	}, asynq.MaxRetry(0), asynq.Timeout(a.activeRunTimeout)); err != nil {
		return "", fmt.Errorf("enqueue passive followup: %w", err)
	}
	return tid, nil
}

// AbortConversationScan 满足 httpapi.AbortAPI：abort 会话关联的 task。
func (a *activeScanAdapter) AbortConversationScan(ctx context.Context, convID string) error {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.TaskID == "" {
		return fmt.Errorf("conversation 无关联 task")
	}
	return a.tasks.Abort(ctx, conv.TaskID, "用户停止")
}

// DeleteConversation 满足 httpapi.ConversationDeleter：删会话+消息，但关联 task 仍在跑时拒删。
// 「先停后删」的服务端把关——返回 httpapi.ErrConversationScanActive → handler 映射 409。
// 防「删了会话、扫描脱缰后台跑、UI 再停不掉、还在烧 token」的孤儿（见 reference_deep_subagent_context 同源思路：
// 不变量在服务端守，不靠前端）。仅在确证 active 时拦截；无 task / 终态 / task 读不到则照常删。
func (a *activeScanAdapter) DeleteConversation(ctx context.Context, convID string) error {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.TaskID != "" {
		if tk, gerr := a.tasks.GetByID(ctx, conv.TaskID); gerr == nil && tk.Status == task.StatusActive {
			return httpapi.ErrConversationScanActive
		}
	}
	return a.conversations.DeleteConversation(ctx, convID)
}

// HandleMessage 满足 httpapi.FollowUpAPI：落 user 消息 → 判意图 → qa 答 / action 续接。
func (a *activeScanAdapter) HandleMessage(ctx context.Context, convID, content string) (string, bool, error) {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return "", false, err
	}
	if _, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleUser, conversation.KindMessage, content, nil); err != nil {
		return "", false, err
	}

	if conv.TaskID == "" {
		return "", false, fmt.Errorf("conversation 无关联 task")
	}
	tk, err := a.tasks.GetByID(ctx, conv.TaskID)
	if err != nil {
		return "", false, err
	}

	// 意图分流（action/qa）对 active、passive 通用：判定走 light LLM。
	g, err := a.router.For(ctx, "inspector") // light provider
	if err != nil {
		return "", false, err
	}
	switch intent.Classify(ctx, g, content) {
	case intent.IntentAction:
		if tk.Status == task.StatusActive {
			return "action", true, nil // 忙：agent 在跑，本轮指导经 conversationContext 下次读到
		}
		// 按 mode 续接：passive 起 traffic-analysis（带 host + 手敲指令），active 起 orchestrator。
		// 两者都 Reopen 同一 task，finding 累积在这次分析会话里（不新建 task）。
		if tk.Mode == task.ModePassive {
			if _, err := a.FollowUpPassive(ctx, conv.TaskID, convID, tk.TargetHost, content); err != nil {
				return "", false, err
			}
		} else if _, err := a.FollowUpScan(ctx, conv.TaskID, convID, "", content); err != nil {
			return "", false, err
		}
		return "action", false, nil
	default: // qa：就已有 finding/流量提问，active/passive 同一套问答
		if err := qa.New(a).Answer(ctx, convID, conv.TaskID, content); err != nil {
			return "", false, err
		}
		return "qa", false, nil
	}
}

// ---- qa.Deps 实现 ----

// FindingsSummary 满足 qa.Deps：把 task 黑板 finding 渲染成文本摘要。
func (a *activeScanAdapter) FindingsSummary(ctx context.Context, _, taskID string) (string, error) {
	fs, err := a.findings.ListByTask(ctx, taskID)
	if err != nil {
		return "", err
	}
	if len(fs) == 0 {
		return "（暂无 finding）", nil
	}
	var b strings.Builder
	for i, f := range fs {
		fmt.Fprintf(&b, "%d. [%s] %s\n", i+1, f.Severity, f.Summary)
	}
	return b.String(), nil
}

// Generate 满足 qa.Deps：调 light provider。
func (a *activeScanAdapter) Generate(ctx context.Context, msgs []llm.Message, tools []llm.ToolSchema) (llm.Result, error) {
	g, err := a.router.For(ctx, "inspector")
	if err != nil {
		return llm.Result{}, err
	}
	return g.Generate(ctx, msgs, tools)
}

// AppendAssistant 满足 qa.Deps：落 assistant 消息，返回 SSE payload。
func (a *activeScanAdapter) AppendAssistant(ctx context.Context, convID, content string) ([]byte, error) {
	msg, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleAssistant, conversation.KindMessage, content, nil)
	if err != nil {
		return nil, err
	}
	return json.Marshal(msg)
}

// Publish 满足 qa.Deps：推 SSE。
func (a *activeScanAdapter) Publish(ctx context.Context, convID string, payload []byte) error {
	return a.publisher.Publish(ctx, convID, payload)
}

// CreateActiveScan 满足 httpapi.ActiveScanAPI（无会话的纯后台扫描入口）。
func (a *activeScanAdapter) CreateActiveScan(ctx context.Context, brief string) (string, string, error) {
	return a.createScan(ctx, brief, "", "")
}

// StartChatScan 满足 httpapi.ChatAPI：建会话（记 role_id）+ 落用户首条消息 + 发起扫描
// （入队带 conversationID + scenarioID）+ 关联会话与 scan。返回 conversationID 供前端订阅 SSE。
// roleID 空时用默认 active 场景兜底。
func (a *activeScanAdapter) StartChatScan(ctx context.Context, brief, roleID string) (string, string, error) {
	if roleID == "" {
		roleID = a.defaultActiveRoleID()
	}
	conv, err := a.conversations.CreateConversation(ctx, briefTitle(brief), "", roleID)
	if err != nil {
		return "", "", fmt.Errorf("create conversation: %w", err)
	}
	if _, err := a.conversations.AppendMessage(ctx, conv.ID, conversation.RoleUser, conversation.KindMessage, brief, nil); err != nil {
		return "", "", fmt.Errorf("append user message: %w", err)
	}
	taskID, _, err := a.createScan(ctx, brief, conv.ID, roleID)
	if err != nil {
		return "", "", err
	}
	if err := a.conversations.LinkTask(ctx, conv.ID, taskID); err != nil {
		return "", "", fmt.Errorf("link task: %w", err)
	}
	// 异步生成智能标题（light LLM 把 brief 总结成短标题）——不阻塞会话创建响应；
	// 失败则保留 briefTitle 截断兜底。前端下次 refresh 列表即见新标题。
	go a.genTitle(conv.ID, brief)
	return conv.ID, taskID, nil
}

// genTitle 用 light LLM 把 brief 总结成 ≤16 字的简短标题，回填 conversation.title。
// 异步调用（独立 context，不随请求结束被 cancel）；失败静默（保留 briefTitle 兜底）。
func (a *activeScanAdapter) genTitle(convID, brief string) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	g, err := a.router.For(ctx, "inspector") // light provider，便宜
	if err != nil {
		return
	}
	prompt := "把下面的渗透测试任务描述总结成一个不超过 16 字的简短中文标题，" +
		"突出目标和测试类型（如「DVWA SQL注入渗透」）。只输出标题本身，不要引号、不要解释。\n\n任务：" + brief
	res, err := g.Generate(ctx, []llm.Message{{Role: llm.RoleUser, Content: prompt}}, nil)
	if err != nil {
		return
	}
	title := strings.TrimSpace(strings.Trim(strings.TrimSpace(res.Content), `"'「」`))
	if r := []rune(title); len(r) > 24 { // 防 LLM 超长，硬截兜底
		title = string(r[:24])
	}
	if title == "" {
		return
	}
	_ = a.conversations.SetTitle(ctx, convID, title)
}

// briefTitle 取 brief 前 40 字（rune 安全，不截半个中文）作会话标题。
func briefTitle(brief string) string {
	const maxRunes = 40
	r := []rune(brief)
	if len(r) > maxRunes {
		return string(r[:maxRunes])
	}
	return brief
}
