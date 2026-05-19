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
	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/graphview"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/passivesession"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/V3teran/liusha/web"

	"github.com/hibiken/asynq"
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
	engStore := engagement.NewStore(pool)
	passiveSessionStore := passivesession.NewStore(pool) // 双轨期新表 store
	activeScanStore := activescan.NewStore(pool)
	findStore := finding.NewStore(pool)
	projector := &graphview.Projector{Findings: findStore, Engagements: engStore}
	invocationStore := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	defer func() { _ = invocationStore.Close() }()

	// Active 模式装配：agentrun store + asynq 入队器。
	// 计数 best-effort 维护到 engagement.agent_run_count（与 scanner 一致）。
	taskStore := agentrun.NewStore(pool).WithCounter(engStore)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")})
	defer enq.Close()
	activeAdapter := &activeScanAdapter{engs: engStore, activeScans: activeScanStore, tasks: taskStore, enq: enq}

	// 监听地址：优先 ENV（运维临时切换）→ yaml。
	listenAddr := envx.OrDefault("LIUSHA_API_ADDR", cfg.API.ListenAddr)
	srv := &http.Server{
		Addr: listenAddr,
		Handler: httpapi.NewServer(httpapi.Deps{
			APIKey:            os.Getenv("LIUSHA_API_KEY"),
			Credentials:       credAPI,
			Engagements: engagementAPIAdapter{
				s:          engStore,
				passive:    passiveSessionStore,
				active:     activeScanStore,
				passiveTTL: time.Duration(cfg.Engagement.MaxAgeHours) * time.Hour,
			},
			Graph:             projector,
			Invocations:       invocationStore,
			AgentRuns:         taskStore, // viewer 拼父子树用（按 parent_id）
			ActiveScan:        activeAdapter,
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

// engagementAPIAdapter 把 *engagement.Store 适配到 httpapi.EngagementsAPI 窄接口。
//
// HTTP API 不暴露 errMsg：用户主动取消 engagement 即视为正常结束，
// abort 调用恒传 ""；store 层完整签名（含 errMsg）保留给 scanner 内部用。
//
// passiveTTL 来自 cfg.Engagement.MaxAgeHours——passive session 新建时写入 expires_at。
type engagementAPIAdapter struct {
	s          *engagement.Store
	passive    *passivesession.Store // 双轨读路径：List 合并新表，Abort 试两表
	active     *activescan.Store
	passiveTTL time.Duration
}

// Abort 双试：先 passive 表，否则 active 表；都没命中则报错。
// HTTP API 不区分 mode，用户只给 ID 不给 type，故试两表是必要的。
// errMsg 恒空（用户主动 abort 视为正常结束；store 完整签名留给 scanner 内部用）。
func (a engagementAPIAdapter) Abort(ctx context.Context, id string) error {
	if _, err := a.passive.GetByID(ctx, id); err == nil {
		return a.passive.Abort(ctx, id, "")
	}
	if _, err := a.active.GetByID(ctx, id); err == nil {
		return a.active.Abort(ctx, id, "")
	}
	return fmt.Errorf("session %s not found in passive_session or active_scan", id)
}

func (a engagementAPIAdapter) EnsurePassiveSession(ctx context.Context) (string, error) {
	eng, err := a.s.LookupOrCreatePassiveSession(ctx, a.passiveTTL)
	if err != nil {
		return "", err
	}
	return eng.ID, nil
}

// List 合并 passive_session + active_scan 两新表 → httpapi.EngagementSummary。
//
// 双轨期：旧 engagement.Store 不再读，由新表数据直接返回。每个 sub-list 各取 limit 条，
// 合并后按 CreatedAt desc 排，最终截到 limit。Mode 字段标记来源表（"passive"/"active"），
// 前端 viewer 用 Mode 区分展示。
func (a engagementAPIAdapter) List(ctx context.Context, limit int) ([]httpapi.EngagementSummary, error) {
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

	out := make([]httpapi.EngagementSummary, 0, len(passives)+len(actives))
	for _, p := range passives {
		scopeJSON, _ := json.Marshal(map[string]string{"host": p.Host})
		s := httpapi.EngagementSummary{
			ID:            p.ID,
			Scope:         string(scopeJSON),
			Status:        string(p.Status),
			Mode:          "passive",
			FlowCount:     p.FlowCount,
			FindingCount:  p.FindingCount,
			AgentRunCount: p.AgentRunCount,
			CreatedAt:     p.CreatedAt.Format(time.RFC3339),
			ExpiresAt:     p.ExpiresAt.Format(time.RFC3339),
			ErrorMessage:  p.ErrorMessage,
		}
		if p.EndedAt != nil {
			s.EndedAt = p.EndedAt.Format(time.RFC3339)
		}
		out = append(out, s)
	}
	for _, sc := range actives {
		scopeJSON, _ := json.Marshal(map[string]string{"brief": sc.Brief})
		s := httpapi.EngagementSummary{
			ID:            sc.ID,
			Scope:         string(scopeJSON),
			Status:        string(sc.Status),
			Mode:          "active",
			FindingCount:  sc.FindingCount,
			AgentRunCount: sc.AgentRunCount,
			CreatedAt:     sc.CreatedAt.Format(time.RFC3339),
			ErrorMessage:  sc.ErrorMessage,
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

// activeScanAdapter 把 engagement.Store + agentrun.Store + worker.Client 组合成
// httpapi.ActiveScanAPI 一站式入口：建 active engagement → 建 hunter agent_run → 入 asynq 队列。
//
// 任一步失败都不留中间状态（前面失败直接返错；engagement 已建但 enqueue 失败会留
// active engagement，由用户手动 abort 或后续 sweeper——保持简单不上事务，与
// passive 模式 ingestor.enqueueMain 一致语义）。
// 双轨期：engs（旧 unified）与 activeScans（新 polymorphic）并存。
// CreateActiveScan 同时建两边表行；新行 ID 作为 agent_run.owner_id 落库。
type activeScanAdapter struct {
	engs        *engagement.Store
	activeScans *activescan.Store
	tasks       *agentrun.Store
	enq         *worker.Client
}

func (a *activeScanAdapter) CreateActiveScan(ctx context.Context, brief string) (string, string, error) {
	// scope 与 entrypoint 都只装 brief 原文——目标 URL / host 由 hunter LLM
	// 从 brief 自然语言里自行识别（不在 API 层做 NL parser）。
	body, err := json.Marshal(map[string]string{"brief": brief})
	if err != nil {
		return "", "", fmt.Errorf("marshal brief: %w", err)
	}
	eng, err := a.engs.CreateActiveSession(ctx, body)
	if err != nil {
		return "", "", err
	}
	// 双轨：新 active_scan 表行；target_host 留空（暂不在 API 层 parse brief）。
	// 失败时 silent fallback——双轨期允许部分 active 缺失新 owner_id，旧 engagement 仍生效。
	// adapter 无 logger 注入，故不打日志；commit B5 切读时该路径已转主，失败应升级 fail-fast。
	activeScanID := ""
	if sc, asErr := a.activeScans.Create(ctx, brief, ""); asErr == nil {
		activeScanID = sc.ID
	}
	payloadInput, err := json.Marshal(map[string]any{
		"mode":       "active",
		"entrypoint": json.RawMessage(body),
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}

	tid, err := a.tasks.Create(ctx, agentrun.NewParams{
		EngagementID: eng.ID,
		OwnerType:    "active_scan", // 双轨：空 activeScanID 由 store NULLIF 折成 NULL
		OwnerID:      activeScanID,
		Role:         string(worker.RoleHunter),
		Input:        payloadInput,
	})
	if err != nil {
		return "", "", fmt.Errorf("create agent_run: %w", err)
	}

	// active 父任务跑 ~4h，asynq 默认 retry 25 次 → 4 天死循环；且 retry 接管时
	// 新 scanner 进程 parentRegistries 是空的，PreDoneCheck 永放行，旧 PG 子留
	// status=running 僵尸态 + viewer 看到"父 done + 子 running"矛盾。
	// MaxRetry(0)：active 父跑挂就跑挂，让用户手动 abort + 重新触发，不重试。
	if _, _, err := a.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		TaskID:       tid,
		EngagementID: eng.ID,
		OwnerType:    "active_scan", // 双轨：activeScanID 空时 OwnerID 空，JSON omit
		OwnerID:      activeScanID,
		Input:        payloadInput,
	}, asynq.MaxRetry(0)); err != nil {
		return "", "", fmt.Errorf("enqueue: %w", err)
	}
	return eng.ID, tid, nil
}
