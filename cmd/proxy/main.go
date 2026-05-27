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
	publicAddr := envx.OrDefault("LIUSHA_PROXY_LISTEN_ADDR", proxyCfg.ListenAddr)
	internalAddr := envx.OrDefault("LIUSHA_PROXY_INTERNAL_ADDR", proxyCfg.InternalAddr)
	agentPublicAddr := envx.OrDefault("LIUSHA_PROXY_AGENT_LISTEN_ADDR", proxyCfg.AgentListenAddr)
	agentInternalAddr := envx.OrDefault("LIUSHA_PROXY_AGENT_INTERNAL_ADDR", proxyCfg.AgentInternalAddr)
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

	// Internal（agent 入口）：共享 filter/publisher/certDir，独立 listener + source 标签。
	// ingestor 端按 snap.Source 分流：internal → 不触发 passive tracker。
	internalServer, err := proxy.NewServer(proxy.ServerDeps{
		Filter:     trafficFilter,
		Publisher:  publisher,
		Cfg:        proxyCfg,
		ListenAddr: agentInternalAddr,
		CertDir:    certDir,
		Source:     "internal",
		Logger:     logger.With().Str("listener", "internal").Logger(),
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("new internal mitm proxy")
	}

	proxyCtx, proxyCancel := context.WithCancel(context.Background())
	defer proxyCancel()

	// healthz HTTP：默认 :9091，避免与 scanner :9090 冲突。
	hsAddr := envx.OrDefault("LIUSHA_PROXY_HEALTHZ_ADDR", proxyCfg.HealthzAddr)
	hsMux := http.NewServeMux()
	hsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	// CDP capture ingest endpoint（v33+ chromium 不再经 proxy，改 CDP Network 主动抓 + push）。
	// ENV LIUSHA_INGEST_TOKEN > yaml proxy.ingest_token；空 = 不强制鉴权（dev）。
	ingestToken := envx.OrDefault("LIUSHA_INGEST_TOKEN", proxyCfg.IngestToken)
	hsMux.HandleFunc("/internal/v1/flows/ingest",
		newIngestHandler(publisher, ingestToken, logger.With().Str("component", "cdp_ingest").Logger()))
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

	// External proxify + sanitizer（passive 入口）
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

	// Internal proxify + sanitizer（agent 入口）
	go func() {
		logger.Info().Str("loopback", agentInternalAddr).Str("source", "internal").Msg("mitm proxy starting")
		if err := internalServer.Run(proxyCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("internal mitm proxy exited")
		}
	}()
	go func() {
		logger.Info().Str("public", agentPublicAddr).Str("upstream", agentInternalAddr).Str("source", "internal").Msg("uri sanitizer listening")
		// internal listener：URI patch + require auth + 解 hunter_id → X-Liusha-Hunter-Id。
		// v33+：chromium 不再经此 proxy（改 CDP Network capture 直推 ingest endpoint），
		// 本路径只承载 CLI 工具（curl/httpx/sqlmap...）—— HTTP_PROXY env 内嵌 user:pass，
		// 主动带 Proxy-Authorization → sanitizer 解出 hunter_id → onResponse 入字典。
		if err := proxy.RunAgentSanitizer(agentPublicAddr, agentInternalAddr); err != nil {
			logger.Error().Err(err).Msg("internal uri sanitizer exited")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	logger.Info().Str("signal", sig.String()).Msg("proxy shutting down")

	externalServer.Stop()
	internalServer.Stop()
	proxyCancel()
	shutdownTimeout := time.Duration(proxyCfg.ShutdownTimeoutSeconds) * time.Second
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := hs.Shutdown(shutdownCtx); err != nil {
		logger.Error().Err(err).Msg("healthz shutdown")
	}
	logger.Info().Msg("proxy stopped")
}

