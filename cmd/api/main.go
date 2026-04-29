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
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/logx"
)

// shutdownTimeout 是收到 SIGINT/SIGTERM 后给 in-flight 请求的最长 drain 时间。
const shutdownTimeout = 5 * time.Second

func main() {
	logger := logx.New("api")
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

	rdb, err := db.NewRedis(ctx, os.Getenv("LIUSHA_REDIS_ADDR"))
	if err != nil {
		logger.Fatal().Err(err).Msg("redis")
	}
	defer func() { _ = rdb.Close() }()

	credAPI := credential.NewRedis(rdb)
	engAPI := engagement.NewStore(pool)

	srv := &http.Server{
		Addr: envOr("LIUSHA_API_ADDR", "0.0.0.0:8080"),
		Handler: httpapi.NewServer(httpapi.Deps{
			APIKey:      os.Getenv("LIUSHA_API_KEY"),
			Credentials: credAPI,
			Engagements: engAPI,
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
