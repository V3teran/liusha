// Package main 是 liusha proxy 进程入口：内嵌 proxify SDK 的 MITM 代理 +
// filter 责任链 + Redis Stream 生产者。
//
// 进程职责（业界最佳实践，Stream-based 解耦）：
//  1. config.Load + db.NewRedis 启动依赖（不再连 PG，proxy 是无状态生产者）
//  2. filter.NewTrafficFilter
//  3. proxy.NewPublisher（XADD 到 Redis Stream `liusha:flow_events`，MAXLEN ~ 100k）
//  4. proxy.NewServer + Run（监听 LIUSHA_PROXY_LISTEN_ADDR，默认 0.0.0.0:8888）
//  5. graceful shutdown（SIGINT/SIGTERM → proxyServer.Stop + 关 redis）
//
// 纯 MITM passive 入口：无 healthz HTTP（存活探 TCP 8888）；active 抓流量 ingest endpoint 已迁到 cmd/runner。
//
// 业务逻辑（proxy_traffic 落库 by host / 聚合器建 passive task / Asynq 入队）
// 全部在 cmd/runner 内的 ingestor 包，proxy 只生产事件不做存储。
package main

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

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

	// proxy 是纯 MITM ingress（filter + XADD），从不调 LLM → 跳过 LLM provider key 校验，
	// 不再强塞一堆用不到的 key 才能启动。
	cfg, err := config.LoadWithoutLLMKeys(envx.OrDefault("LIUSHA_CONFIG", "./config/config.yaml"))
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
	// 双 listener（0060+）物理隔离 source，避免 agent 自挖流量触发 passive trafficAnalysis 自激震荡：
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

	// 注：proxy 不再起 healthz HTTP server / ingest endpoint。
	//   - active 抓流量 ingest endpoint 已迁到 cmd/runner（沙箱回连 runner :9090）。
	//   - 存活检测改 TCP 探 8888（MITM listen 口），见 docker-compose / run-svc。
	// proxy 现在 = 纯 MITM passive 入口（external listener + sanitizer）。

	// External proxify + sanitizer（passive 入口；唯一 listener）
	go func() {
		logger.Info().Str("loopback", internalAddr).Str("source", "external").Msg("mitm proxy starting")
		if err := externalServer.Run(proxyCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("external mitm proxy exited")
		}
	}()
	go func() {
		logger.Info().Str("public", publicAddr).Str("upstream", internalAddr).Str("source", "external").Msg("uri sanitizer listening")
		// external listener：外部 passive 流量本就无凭证，仅做 URI patch。
		// 流量按 host 落 proxy_traffic，passive task 由 ingestor 聚合器按窗口生成。
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
	logger.Info().Msg("proxy stopped")
}
