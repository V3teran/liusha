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
	"sort"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/activescan"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/hunter"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/owner"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/scenario"
	"github.com/V3teran/liusha/internal/sitemap"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/V3teran/liusha/web"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

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
	passiveSessionStore := passivesession.NewStore(pool)
	activeScanStore := activescan.NewStore(pool)
	findStore := finding.NewStore(pool)
	// projector 只读 DistinctRoutesWithRepresentative（自带 body 片段抽 title），body 截断参数无关 → 0,0。
	flowStore := flow.NewStore(pool, 0, 0)
	projector := &sitemap.Projector{
		Findings: findStore,
		Flows:    flowStore,       // 攻击面从 http_flow(source=internal) 派生，不再依赖 endpoint 表
		Active:   activeScanStore, // sitemap 仅 active 模式（passive 用 findings 列表）
	}
	invocationStore := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	defer func() { _ = invocationStore.Close() }()

	// Active 模式装配：hunter store + asynq 入队器。
	taskStore := hunter.NewStore(pool)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")})
	defer enq.Close()
	auditStore := audit.NewStore(pool)       // 0047：owner abort / create 审计
	convStore := conversation.NewStore(pool) // 阶段B：对话/消息
	// 阶段C：场景 role（roles/*.md）。加载失败仅警告——/roles 返回空、/chat 用空 role 兜底，
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
	activeAdapter := &activeScanAdapter{activeScans: activeScanStore, tasks: taskStore, enq: enq, audit: auditStore, conversations: convStore, roles: scenarioRoles}

	// SSE stream cookie 密钥：对话功能开启时必填（EventSource 鉴权用），缺失 fail-fast。
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
			Owners: ownerAPIAdapter{
				passive:    passiveSessionStore,
				active:     activeScanStore,
				audit:      auditStore,
				passiveTTL: time.Duration(cfg.Session.MaxAgeHours) * time.Hour,
			},
			Sitemap:           projector,
			Invocations:       invocationStore,
			AgentRuns:         taskStore, // viewer 拼任务树用（按 parent_id）
			ActiveScan:        activeAdapter,
			Chat:              activeAdapter,                // 阶段B：POST /chat 对话发起扫描
			FollowUp:          activeAdapter,                // 多轮：POST /conversations/:id/messages 动作续接
			Abort:             activeAdapter,                // 多轮：POST /conversations/:id/abort 停止对话关联扫描
			Conversations:     convStore,                    // 阶段B：对话列表 / 消息回看
			EventStream:       eventStreamAdapter{rdb: rdb}, // 阶段B：SSE 订阅 redis 事件
			Roles:             activeAdapter,                // 阶段C：GET /roles 场景列表
			StaticFS:          web.ViewerFS(),
			EnableDevAutofill: envx.OrDefault("LIUSHA_VIEWER_DEV_KEY", "") != "",
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

// ownerAPIAdapter 把 owner store (passive_session/active_scan) 适配到 httpapi.OwnersAPI 窄接口。
//
// HTTP API 不暴露 errMsg：用户主动取消  owner 即视为正常结束，
// abort 调用恒传 ""；store 层完整签名（含 errMsg）保留给 scanner 内部用。
//
// passive_session 现在 per-host：EnsurePassiveSession(host) 调 LookupOrCreate；
// host 空时返空 id 兼容旧 mitmproxy 预热路径。
type ownerAPIAdapter struct {
	passive    *passivesession.Store // List 合并新表，Abort 试两表
	active     *activescan.Store
	audit      *audit.Store  // 0047：abort 写审计事件；nil 时跳过（向后兼容）
	passiveTTL time.Duration // passive_session 创建 ttl（来自 cfg.Session.MaxAgeHours）
}

// Abort 双试：先 passive 表，否则 active 表；都没命中则报错。
// HTTP API 不区分 mode，用户只给 ID 不给 type，故试两表是必要的。
// errMsg 恒空（用户主动 abort 视为正常结束；store 完整签名留给 scanner 内部用）。
// 0047：成功 abort 后写 audit_log（actor=api_user，target_kind=passive_session/active_scan）。
func (a ownerAPIAdapter) Abort(ctx context.Context, id string) error {
	if _, err := a.passive.GetByID(ctx, id); err == nil {
		if err := a.passive.Abort(ctx, id, ""); err != nil {
			return err
		}
		a.writeAudit(ctx, audit.ActionOwnerAbort, owner.Passive, id)
		return nil
	}
	if _, err := a.active.GetByID(ctx, id); err == nil {
		if err := a.active.Abort(ctx, id, ""); err != nil {
			return err
		}
		a.writeAudit(ctx, audit.ActionOwnerAbort, owner.Active, id)
		return nil
	}
	return fmt.Errorf("session %s not found in passive_session or active_scan", id)
}

// writeAudit best-effort 写审计事件；失败仅吞错不阻塞业务。
func (a ownerAPIAdapter) writeAudit(ctx context.Context, action, kind, id string) {
	if a.audit == nil {
		return
	}
	_, _ = a.audit.Append(ctx, audit.Event{
		Actor:      audit.ActorAPIUser,
		Action:     action,
		TargetKind: kind,
		TargetID:   id,
	})
}

// EnsurePassiveSession 按 host 找/建 active passive_session 返其 id。
// host 空时返空 id（向后兼容旧 mitmproxy 预热路径"代理就绪"信号）；
// host 非空时调 passive.LookupOrCreate(host, passiveTTL)，跟 ingestor 流量入口走同一 store 方法，
// 保证 e2e 启动期预创建 + ingestor 后续流量驱动两条路径幂等（同一 host 返同一 id）。
func (a ownerAPIAdapter) EnsurePassiveSession(ctx context.Context, host string) (string, error) {
	if host == "" {
		return "", nil
	}
	sess, err := a.passive.LookupOrCreate(ctx, host, a.passiveTTL)
	if err != nil {
		return "", fmt.Errorf("LookupOrCreate passive_session for host=%s: %w", host, err)
	}
	return sess.ID, nil
}

// List 合并 passive_session + active_scan 两新表 → httpapi.OwnerSummary。
//
// 双轨期：旧 owner store 不再读，由新表数据直接返回。每个 sub-list 各取 limit 条，
// 合并后按 CreatedAt desc 排，最终截到 limit。Mode 字段标记来源表（"passive"/"active"），
// 前端 viewer 用 Mode 区分展示。
func (a ownerAPIAdapter) List(ctx context.Context, limit int) ([]httpapi.OwnerSummary, error) {
	if limit <= 0 {
		limit = 20
	}
	// 各取 limit 条；后面合并截断保证 desc 排序正确（边界 case：单表都新过另一表的最旧 N）。
	passives, err := a.passive.List(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list passive sessions: %w", err)
	}
	actives, err := a.active.List(ctx, limit)
	if err != nil {
		return nil, fmt.Errorf("list active scans: %w", err)
	}

	out := make([]httpapi.OwnerSummary, 0, len(passives)+len(actives))
	for _, p := range passives {
		scopeJSON, _ := json.Marshal(map[string]string{"host": p.Host})
		s := httpapi.OwnerSummary{
			ID:           p.ID,
			Scope:        string(scopeJSON),
			Status:       string(p.Status),
			Mode:         "passive",
			CreatedAt:    p.CreatedAt.Format(time.RFC3339),
			ExpiresAt:    p.ExpiresAt.Format(time.RFC3339),
			ErrorMessage: p.ErrorMessage,
		}
		if p.EndedAt != nil {
			s.EndedAt = p.EndedAt.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	for _, sc := range actives {
		scopeJSON, _ := json.Marshal(map[string]string{"brief": sc.Brief})
		s := httpapi.OwnerSummary{
			ID:           sc.ID,
			Scope:        string(scopeJSON),
			Status:       string(sc.Status),
			Mode:         "active",
			CreatedAt:    sc.CreatedAt.Format(time.RFC3339),
			ErrorMessage: sc.ErrorMessage,
		}
		if sc.EndedAt != nil {
			s.EndedAt = sc.EndedAt.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	// 按 CreatedAt desc 排——比较字符串即可（RFC3339 lexicographic order = chronological order）。
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt > out[j].CreatedAt })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// activeScanAdapter 把 owner store + hunter.Store + worker.Client 组合成
// httpapi.ActiveScanAPI 一站式入口：建 active scan → 建 hunter agent_run → 入 asynq 队列。
//
// 任一步失败都不留中间状态（前面失败直接返错；owner 已建但 enqueue 失败会留
// active scan，由用户手动 abort 或后续 sweeper——保持简单不上事务，与
// passive 模式 ingestor.enqueueMain 一致语义）。
// 单源：仅 active_scan 表（0040 DROP FK 后旧 owner 路径正式弃用）。
type activeScanAdapter struct {
	activeScans   *activescan.Store
	tasks         *hunter.Store
	enq           *worker.Client
	audit         *audit.Store        // 0047：create 写审计事件；nil 跳过
	conversations *conversation.Store // 阶段B：StartChatScan 建对话；nil 时仅 CreateActiveScan 可用
	roles         []scenario.Role     // 阶段C：场景 role（StartChatScan 默认兜底 + ListRoles 暴露）
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

// createScan 是建 active scan 的核心：建 active_scan + hunter run + 入 asynq 队列（带
// conversationID）。CreateActiveScan（无对话纯后台）与 StartChatScan（对话发起）共用。
func (a *activeScanAdapter) createScan(ctx context.Context, brief, conversationID, scenarioID string) (string, string, error) {
	// scope 与 entrypoint 都只装 brief 原文——目标 URL / host 由 hunter LLM
	// 从 brief 自然语言里自行识别（不在 API 层做 NL parser）。
	body, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", "", fmt.Errorf("marshal brief: %w", err)
	}
	// 单源：仅写 active_scan 表（旧 owner 路径在 0040 FK DROP 后正式弃用）。
	// target_host 留空——暂不在 API 层 parse brief，hunter LLM 从 brief 自识别。
	sc, err := a.activeScans.Create(ctx, brief, "")
	if err != nil {
		return "", "", fmt.Errorf("create active_scan: %w", err)
	}
	payloadInput, err := json.Marshal(map[string]any{
		"mode":       "active",
		"entrypoint": json.RawMessage(body),
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}

	tid, err := a.tasks.Create(ctx, hunter.NewParams{
		OwnerType: owner.Active,
		OwnerID:   sc.ID,
		Role:      "orchestrator",
		Input:     payloadInput,
	})
	if err != nil {
		return "", "", fmt.Errorf("create hunter run: %w", err)
	}

	// active orchestrator跑 ~4h，asynq 默认 retry 25 次 → 4 天死循环；且 retry 接管时
	// 新 scanner 进程 parentRegistries 是空的，PreDoneCheck 永放行，旧 PG exploitation 留
	// status=running 僵尸态 + viewer 看到"orchestrator done + exploitation running"矛盾。
	// MaxRetry(0)：orchestrator跑挂就跑挂，让用户手动 abort + 重新触发，不重试。
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       tid,
		OwnerType:      owner.Active,
		OwnerID:        sc.ID,
		ConversationID: conversationID, // 阶段B：对话发起时非空 → scanner 发过程事件
		ScenarioID:     scenarioID,     // 阶段C：场景 role → scanner 注入主代理人设
		Input:          payloadInput,
	}, asynq.MaxRetry(0)); err != nil {
		return "", "", fmt.Errorf("enqueue: %w", err)
	}

	// 0047：active scan 创建成功 → 审计事件。metadata 记 brief 前 200 字便于事后查
	// （完整 brief 在 active_scan.brief 列）。
	if a.audit != nil {
		briefPreview := brief
		if len(briefPreview) > 200 {
			briefPreview = briefPreview[:200]
		}
		meta, _ := json.Marshal(map[string]string{"brief_preview": briefPreview, "hunter_id": tid})
		if _, err := a.audit.Append(ctx, audit.Event{
			Actor:      audit.ActorAPIUser,
			Action:     audit.ActionOwnerCreate,
			TargetKind: owner.Active,
			TargetID:   sc.ID,
			Metadata:   meta,
		}); err != nil {
			// best-effort：审计失败不阻塞业务返回
			_ = err
		}
	}

	return sc.ID, tid, nil
}

// FollowUpScan 在已有 active_scan 上发起一次续接 run（多轮动作）：重开 scan + 建 hunter run +
// 入队（brief=追加消息）。复用 owner 作用域黑板——新 orchestrator 经 BuildUserPrompt 看到先前 finding/notes。
// 入队 Payload 与 createScan 同构，仅 OwnerID 复用传入 scanID、不新建 active_scan。
func (a *activeScanAdapter) FollowUpScan(ctx context.Context, scanID, conversationID, scenarioID, brief string) (string, error) {
	if err := a.activeScans.Reopen(ctx, scanID); err != nil {
		return "", fmt.Errorf("reopen scan: %w", err)
	}
	body, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", fmt.Errorf("marshal brief: %w", err)
	}
	payloadInput, err := json.Marshal(map[string]any{"mode": "active", "entrypoint": json.RawMessage(body)})
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}
	tid, err := a.tasks.Create(ctx, hunter.NewParams{
		OwnerType: owner.Active,
		OwnerID:   scanID,
		Role:      "orchestrator",
		Input:     payloadInput,
	})
	if err != nil {
		return "", fmt.Errorf("create hunter run: %w", err)
	}
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:       tid,
		OwnerType:      owner.Active,
		OwnerID:        scanID,
		ConversationID: conversationID,
		ScenarioID:     scenarioID,
		Input:          payloadInput,
	}, asynq.MaxRetry(0)); err != nil {
		return "", fmt.Errorf("enqueue followup: %w", err)
	}
	return tid, nil
}

// GetConversationScan 满足 httpapi.FollowUpAPI：查对话关联 scan + 其状态。
func (a *activeScanAdapter) GetConversationScan(ctx context.Context, convID string) (string, string, error) {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return "", "", err
	}
	if conv.ScanID == "" {
		return "", "", fmt.Errorf("conversation 无关联 scan")
	}
	sc, err := a.activeScans.GetByID(ctx, conv.ScanID)
	if err != nil {
		return "", "", err
	}
	return conv.ScanID, string(sc.Status), nil
}

// AbortConversationScan 满足 httpapi.AbortAPI：abort 对话关联的 active_scan。
func (a *activeScanAdapter) AbortConversationScan(ctx context.Context, convID string) error {
	conv, err := a.conversations.GetConversation(ctx, convID)
	if err != nil {
		return err
	}
	if conv.ScanID == "" {
		return fmt.Errorf("conversation 无关联 scan")
	}
	return a.activeScans.Abort(ctx, conv.ScanID, "用户停止")
}

// AppendUserMessage 满足 httpapi.FollowUpAPI：落用户追加消息。
func (a *activeScanAdapter) AppendUserMessage(ctx context.Context, convID, content string) error {
	_, err := a.conversations.AppendMessage(ctx, convID, conversation.RoleUser, conversation.KindMessage, content, nil)
	return err
}

// CreateActiveScan 满足 httpapi.ActiveScanAPI（无对话的纯后台扫描入口）。
func (a *activeScanAdapter) CreateActiveScan(ctx context.Context, brief string) (string, string, error) {
	return a.createScan(ctx, brief, "", "")
}

// StartChatScan 满足 httpapi.ChatAPI：建对话（记 role_id）+ 落用户首条消息 + 发起扫描
// （入队带 conversationID + scenarioID）+ 关联对话与 scan。返回 conversationID 供前端订阅 SSE。
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
	scanID, _, err := a.createScan(ctx, brief, conv.ID, roleID)
	if err != nil {
		return "", "", err
	}
	if err := a.conversations.LinkScan(ctx, conv.ID, scanID); err != nil {
		return "", "", fmt.Errorf("link scan: %w", err)
	}
	return conv.ID, scanID, nil
}

// briefTitle 取 brief 前 40 字（rune 安全，不截半个中文）作对话标题。
func briefTitle(brief string) string {
	const maxRunes = 40
	r := []rune(brief)
	if len(r) > maxRunes {
		return string(r[:maxRunes])
	}
	return brief
}
