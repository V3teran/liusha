// Package main 启动 liusha-api 服务：装配配置、日志、数据库连接池与 HTTP 服务，
// 并监听 SIGINT/SIGTERM 以触发 10 秒超时内的优雅关停。
package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/config"
	engstore "github.com/V3teran/liusha/internal/engagement/store"
	"github.com/V3teran/liusha/internal/httpapi"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	cfgPath := os.Getenv("LIUSHA_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/config.yaml"
	}

	cfg, err := config.Load(cfgPath)
	if err != nil {
		// 配置加载失败属于启动期致命错误；此时 logx 尚未初始化，直接 panic 保证可见。
		panic("加载配置失败: " + err.Error())
	}
	// 开发环境打开严格模式：无 trace_id 的日志直接 panic，强制上游补齐链路。
	logx.Init(cfg.LogLevel, cfg.Env == "development")

	// 为启动/关停阶段的日志注入一次性的 trace_id，避免在严格模式下触发 panic。
	bootCtx := logx.WithTraceID(context.Background(), logx.NewTraceID())

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		logx.From(bootCtx).Fatal().Err(err).Msg("连接 postgres 失败")
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		logx.From(bootCtx).Fatal().Err(err).Msg("ping postgres 失败")
	}

	srv := httpapi.New(cfg, engstore.NewPostgres(pool))

	errCh := make(chan error, 1)
	go func() { errCh <- srv.Run() }()

	select {
	case <-ctx.Done():
		logx.From(bootCtx).Info().Msg("收到关停信号，开始优雅退出")
	case err := <-errCh:
		logx.From(bootCtx).Error().Err(err).Msg("http server 意外退出")
	}

	shutdownCtx, cancelShutdown := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelShutdown()
	_ = srv.Shutdown(shutdownCtx)
}
