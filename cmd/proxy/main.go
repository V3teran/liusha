// Package main 是 liusha proxy 进程入口：内嵌 proxify SDK 的 MITM 代理 +
// filter 责任链 + Redis Stream 生产者。
//
// 进程职责（业界最佳实践，Stream-based 解耦）：
//  1. config.Load + db.NewRedis 启动依赖（不再连 PG，proxy 是无状态生产者）
//  2. filter.NewTrafficFilter
//  3. proxy.NewPublisher（XADD 到 Redis Stream `liusha:flow_events`，MAXLEN ~ 100k）
//  4. proxy.NewServer + Run（监听 LIUSHA_PROXY_LISTEN_ADDR，默认 0.0.0.0:8888）
//  5. healthz HTTP（默认 :9091，与 scanner :9090 错开）
//  6. graceful shutdown（SIGINT/SIGTERM → proxyServer.Stop + 关 redis）
//
// 业务逻辑（Rotator.EnsurePassiveSession / flow.Append / Asynq 入队）
// 全部在 cmd/scanner 内的 ingestor 包，proxy 只生产事件不做存储。
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/proxy"
)

func main() {
	logger := logx.New("proxy")
	ctx := context.Background()

	cfg, err := config.Load(envOr("LIUSHA_CONFIG", "./config/config.yaml"))
	if err != nil {
		logger.Fatal().Err(err).Msg("load config")
	}

	redisAddr := os.Getenv("LIUSHA_REDIS_ADDR")
	rdb, err := db.NewRedis(ctx, redisAddr, cfg.Redis)
	if err != nil {
		logger.Fatal().Err(err).Msg("redis")
	}
	defer rdb.Close()

	proxyCfg := cfg.Proxy

	// 内嵌 MITM 代理装配链：filter → publisher → proxy.Server
	// ENV 仍可临时覆盖 yaml；空 ENV → 走 yaml；yaml 也空 → ApplyDefaults 兜底。
	publicAddr := envOr("LIUSHA_PROXY_LISTEN_ADDR", proxyCfg.ListenAddr)
	internalAddr := envOr("LIUSHA_PROXY_INTERNAL_ADDR", proxyCfg.InternalAddr)
	certDir := envOr("LIUSHA_PROXY_CERT_DIR", "") // 空则 proxy.Server 用 $HOME/<cert_subdir>

	trafficFilter := filter.NewTrafficFilter(proxyCfg)
	publisher, err := proxy.NewPublisher(rdb, proxyCfg.StreamName, int64(proxyCfg.StreamMaxLen))
	if err != nil {
		logger.Fatal().Err(err).Msg("new publisher")
	}

	// proxify 监听 internal loopback；公开端口由 sanitizer 接管，
	// 把 relative URI 客户端报文 patch 成 absolute form 后透传给 proxify。
	proxyServer, err := proxy.NewServer(proxy.ServerDeps{
		Filter:     trafficFilter,
		Publisher:  publisher,
		Cfg:        proxyCfg,
		ListenAddr: internalAddr,
		CertDir:    certDir,
		Logger:     logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("new mitm proxy")
	}

	proxyCtx, proxyCancel := context.WithCancel(context.Background())
	defer proxyCancel()

	// healthz HTTP：默认 :9091，避免与 scanner :9090 冲突。
	hsAddr := envOr("LIUSHA_PROXY_HEALTHZ_ADDR", proxyCfg.HealthzAddr)
	hsMux := http.NewServeMux()
	hsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	hs := &http.Server{
		Addr:              hsAddr,
		Handler:           hsMux,
		ReadHeaderTimeout: time.Duration(cfg.API.ReadHeaderTimeoutSeconds) * time.Second,
	}

	go func() {
		logger.Info().Str("addr", hs.Addr).Msg("proxy healthz listening")
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("healthz serve")
		}
	}()

	go func() {
		logger.Info().Str("internal", internalAddr).Msg("mitm proxy starting")
		if err := proxyServer.Run(proxyCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("mitm proxy exited")
		}
	}()

	// sanitizer：公开端口接客户端，patch relative URI 后转给 proxify loopback。
	go func() {
		logger.Info().Str("public", publicAddr).Str("upstream", internalAddr).Msg("uri sanitizer listening")
		if err := proxy.RunSanitizingForwarder(publicAddr, internalAddr); err != nil {
			logger.Error().Err(err).Msg("uri sanitizer exited")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	logger.Info().Str("signal", sig.String()).Msg("proxy shutting down")

	proxyServer.Stop()
	proxyCancel()
	shutdownTimeout := time.Duration(proxyCfg.ShutdownTimeoutSeconds) * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("healthz shutdown")
	}
	logger.Info().Msg("proxy stopped")
}

// envOr 读取环境变量；空则返回 def。
func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
