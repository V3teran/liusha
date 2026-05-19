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
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/graphview"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/V3teran/liusha/web"

	"github.com/hibiken/asynq"
)

func main() {
	logger := logx.New("api")
	ctx := context.Background()

	cfg, err := config.Load(envOr("LIUSHA_CONFIG", "./config/config.yaml"))
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
	findStore := finding.NewStore(pool)
	projector := &graphview.Projector{Findings: findStore, Engagements: engStore}
	invocationStore := llminvocation.NewStoreWithConfig(pool, cfg.LLM.Invocation)
	defer func() { _ = invocationStore.Close() }()

	// Active 模式装配：agentrun store + asynq 入队器。
	// 计数 best-effort 维护到 engagement.agent_run_count（与 scanner 一致）。
	taskStore := agentrun.NewStore(pool).WithCounter(engStore)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: os.Getenv("LIUSHA_REDIS_ADDR")})
	defer enq.Close()
	activeAdapter := &activeScanAdapter{engs: engStore, tasks: taskStore, enq: enq}

	// 监听地址：优先 ENV（运维临时切换）→ yaml。
	listenAddr := envOr("LIUSHA_API_ADDR", cfg.API.ListenAddr)
	srv := &http.Server{
		Addr: listenAddr,
		Handler: httpapi.NewServer(httpapi.Deps{
			APIKey:            os.Getenv("LIUSHA_API_KEY"),
			Credentials:       credAPI,
			Engagements:       engagementAPIAdapter{s: engStore, passiveTTL: time.Duration(cfg.Engagement.MaxAgeHours) * time.Hour},
			Graph:             projector,
			Invocations:       invocationStore,
			AgentRuns:         taskStore, // viewer 拼父子树用（按 parent_id）
			ActiveScan:        activeAdapter,
			StaticFS:          web.ViewerFS(),
			EnableDevAutofill: envOr("LIUSHA_VIEWER_DEV_KEY", "") != "",
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

// envOr 读取环境变量；空则返回 def。
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// engagementAPIAdapter 把 *engagement.Store 适配到 httpapi.EngagementsAPI 窄接口。
//
// HTTP API 不暴露 errMsg：用户主动取消 engagement 即视为正常结束，
// abort 调用恒传 ""；store 层完整签名（含 errMsg）保留给 scanner 内部用。
//
// passiveTTL 来自 cfg.Engagement.MaxAgeHours——passive session 新建时写入 expires_at。
type engagementAPIAdapter struct {
	s          *engagement.Store
	passiveTTL time.Duration
}

func (a engagementAPIAdapter) Abort(ctx context.Context, id string) error {
	return a.s.Abort(ctx, id, "")
}

func (a engagementAPIAdapter) EnsurePassiveSession(ctx context.Context) (string, error) {
	eng, err := a.s.LookupOrCreatePassiveSession(ctx, a.passiveTTL)
	if err != nil {
		return "", err
	}
	return eng.ID, nil
}

// List 适配 engagement.Store.List → httpapi.EngagementSummary。
// 不直接返回 engagement.Engagement 完整结构，避免泄露大字段到前端。
func (a engagementAPIAdapter) List(ctx context.Context, limit int) ([]httpapi.EngagementSummary, error) {
	rows, err := a.s.List(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]httpapi.EngagementSummary, 0, len(rows))
	for _, e := range rows {
		summary := httpapi.EngagementSummary{
			ID:            e.ID,
			Scope:         string(e.Scope),
			Status:        string(e.Status),
			Mode:          string(e.Mode),
			FlowCount:     e.FlowCount,
			FindingCount:  e.FindingCount,
			AgentRunCount: e.AgentRunCount,
			CreatedAt:     e.CreatedAt.Format(time.RFC3339),
			ErrorMessage:  e.ErrorMessage,
		}
		if e.ExpiresAt != nil {
			summary.ExpiresAt = e.ExpiresAt.Format(time.RFC3339)
		}
		if e.EndedAt != nil {
			summary.EndedAt = e.EndedAt.Format(time.RFC3339)
		}
		out = append(out, summary)
	}
	return out, nil
}

// activeScanAdapter 把 engagement.Store + agentrun.Store + worker.Client 组合成
// httpapi.ActiveScanAPI 一站式入口：建 active engagement → 建 hunter agent_run → 入 asynq 队列。
//
// 任一步失败都不留中间状态（前面失败直接返错；engagement 已建但 enqueue 失败会留
// active engagement，由用户手动 abort 或后续 sweeper——保持简单不上事务，与
// passive 模式 ingestor.enqueueMain 一致语义）。
type activeScanAdapter struct {
	engs  *engagement.Store
	tasks *agentrun.Store
	enq   *worker.Client
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
	payloadInput, err := json.Marshal(map[string]any{
		"mode":       "active",
		"entrypoint": json.RawMessage(body),
	})
	if err != nil {
		return "", "", fmt.Errorf("marshal payload: %w", err)
	}

	tid, err := a.tasks.Create(ctx, agentrun.NewParams{
		EngagementID: eng.ID,
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
		Input:        payloadInput,
	}, asynq.MaxRetry(0)); err != nil {
		return "", "", fmt.Errorf("enqueue: %w", err)
	}
	return eng.ID, tid, nil
}
