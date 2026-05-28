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
// 业务逻辑（passive_session.LookupOrCreate by host / flow.Append / Asynq 入队）
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
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/proxy"
)

func main() {
	logger := logx.New("proxy")
	ctx := context.Background()

	cfg, err := config.Load(envx.OrDefault("LIUSHA_CONFIG", "./config/config.yaml"))
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
	//
	// 双 listener（0060+）物理隔离 source，避免 agent 自挖流量触发 passive tracker 自激震荡：
	//   external: publicAddr (8888) → sanitizer → internalAddr loopback (18888) → external proxify
	//   internal: agentPublicAddr (8890) → sanitizer → agentInternalAddr loopback (18890) → internal proxify
	// v34+：删除 agent (internal) listener — chromium 流量改走 CDP capture → ingest endpoint
	// （见下方 newIngestHandler），CLI 工具直连不入字典。仅保留 external listener 给 passive 入口。
	publicAddr := envx.OrDefault("LIUSHA_PROXY_LISTEN_ADDR", proxyCfg.ListenAddr)
	internalAddr := envx.OrDefault("LIUSHA_PROXY_INTERNAL_ADDR", proxyCfg.InternalAddr)
	certDir := envx.OrDefault("LIUSHA_PROXY_CERT_DIR", "") // 空则 proxy.Server 用 $HOME/<cert_subdir>

	trafficFilter := filter.NewTrafficFilter(proxyCfg)
	publisher, err := proxy.NewPublisher(rdb, proxyCfg.StreamName, int64(proxyCfg.StreamMaxLen))
	if err != nil {
		logger.Fatal().Err(err).Msg("new publisher")
	}

	// External（passive 入口）：proxify 监听 internal loopback，公开端口由 sanitizer 接管。
	externalServer, err := proxy.NewServer(proxy.ServerDeps{
		Filter:     trafficFilter,
		Publisher:  publisher,
		Cfg:        proxyCfg,
		ListenAddr: internalAddr,
		CertDir:    certDir,
		Source:     "external",
		Logger:     logger.With().Str("listener", "external").Logger(),
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("new external mitm proxy")
	}

	proxyCtx, proxyCancel := context.WithCancel(context.Background())
	defer proxyCancel()

	// healthz HTTP：默认 :9091，避免与 scanner :9090 冲突。
	// v35+：原 /internal/v1/flows/ingest endpoint 已删（chromium CDP capture 链路砍掉），
	// 本 listener 只剩 /healthz 一个职责，未来如再加 admin endpoint 可挂同一 mux。
	hsAddr := envx.OrDefault("LIUSHA_PROXY_HEALTHZ_ADDR", proxyCfg.HealthzAddr)
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

	// External proxify + sanitizer（passive 入口；v34+ 唯一 listener）
	go func() {
		logger.Info().Str("loopback", internalAddr).Str("source", "external").Msg("mitm proxy starting")
		if err := externalServer.Run(proxyCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("external mitm proxy exited")
		}
	}()
	go func() {
		logger.Info().Str("public", publicAddr).Str("upstream", internalAddr).Str("source", "external").Msg("uri sanitizer listening")
		// external listener：外部 passive 流量本就无凭证，仅做 URI patch。
		// owner_id 关联走 host → passive_session 路径（ingestor LookupOrCreate）。
		if err := proxy.RunPassiveSanitizer(publicAddr, internalAddr); err != nil {
			logger.Error().Err(err).Msg("external uri sanitizer exited")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	logger.Info().Str("signal", sig.String()).Msg("proxy shutting down")

	externalServer.Stop()
	proxyCancel()
	shutdownTimeout := time.Duration(proxyCfg.ShutdownTimeoutSeconds) * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("healthz shutdown")
	}
	logger.Info().Msg("proxy stopped")
}

