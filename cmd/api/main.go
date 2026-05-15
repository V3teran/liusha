// Package main 是 liusha api 进程入口：装配 config / pg / redis / stores / httpapi。
//
// 启动顺序：logger → config（含 ENV 覆盖）→ pg+redis → stores → http server → 监听 SIGINT/SIGTERM。
// 关闭顺序：收到信号后用 5s 超时 ctx 调 srv.Shutdown，再让 defer 关 pool/redis。
package main

import (
	"context"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/graphview"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/web"
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

	// 监听地址：优先 ENV（运维临时切换）→ yaml。
	listenAddr := envOr("LIUSHA_API_ADDR", cfg.API.ListenAddr)
	srv := &http.Server{
		Addr: listenAddr,
		Handler: httpapi.NewServer(httpapi.Deps{
			APIKey:            os.Getenv("LIUSHA_API_KEY"),
			Credentials:       credAPI,
			Engagements:       engagementAPIAdapter{s: engStore, proxyTTL: time.Duration(cfg.Engagement.MaxAgeHours) * time.Hour},
			Graph:             projector,
			Invocations:       invocationStore,
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
// proxyTTL 来自 cfg.Engagement.MaxAgeHours——proxy session 新建时写入 expires_at。
type engagementAPIAdapter struct {
	s        *engagement.Store
	proxyTTL time.Duration
}

func (a engagementAPIAdapter) Abort(ctx context.Context, id string) error {
	return a.s.Abort(ctx, id, "")
}

func (a engagementAPIAdapter) EnsureProxySession(ctx context.Context) (string, error) {
	eng, err := a.s.LookupOrCreateProxySession(ctx, a.proxyTTL)
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
