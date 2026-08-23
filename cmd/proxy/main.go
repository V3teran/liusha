// Package main 是 liusha proxy 进程入口：内嵌 proxify SDK 的 MITM 代理 +
// filter 责任链 + Redis Stream 生产者。
//
// 进程职责（业界最佳实践，Stream-based 解耦）：
//  1. config.Load + db.NewRedis + db.NewPgPool 启动依赖
//  2. settingstore 读 proxy_filter 组（DB 为过滤规则唯一事实源）→ filter.NewTrafficFilter
//  3. proxy.NewPublisher（XADD 到 Redis Stream `liusha:flow_events`，MAXLEN ~ 100k）
//  4. proxy.NewServer + Run（监听 LIUSHA_PROXY_LISTEN_ADDR，默认 0.0.0.0:8888）
//  5. cachestore.Subscribe + OnInvalidate：proxy_filter 组热改经失效总线广播到本进程，
//     重读 settingstore 重建过滤链并 Server.SwapFilter 原子换入（真热改，无需重启）
//  6. graceful shutdown（SIGINT/SIGTERM → proxyServer.Stop + 关 redis + 关 pool）
//
// 为何连 PG：过滤规则（流量过滤黑白名单 + body 上限）是系统业务旋钮，事实源在 system_setting，
// 前端「系统配置」改后经多级缓存跨进程失效——proxy 必须能读 DB + 订阅失效，才能热换规则。
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
	"time"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/config/settingstore"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/envx"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/rs/zerolog"
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

	// PG 连接：过滤规则（proxy_filter 组）事实源在 system_setting，proxy 需读 DB + 订阅失效热换规则。
	pool, err := db.NewPgPool(ctx, os.Getenv("LIUSHA_POSTGRES_DSN"),
		cfg.Postgres.MaxConns, cfg.Postgres.MinConns,
		cfg.Postgres.ConnectTimeoutSeconds, cfg.Postgres.MaxConnLifetimeSeconds)
	if err != nil {
		logger.Fatal().Err(err).Msg("postgres")
	}
	defer pool.Close()

	// 共享多级缓存内核（L1 内存 + L2 redis + 跨进程失效总线）+ 业务旋钮事实源。
	// Subscribe 后台起（阻塞循环），OnInvalidate 回调在收到 proxy_filter 失效时热换过滤链。
	cache := cachestore.New(rdb, 0)
	settingStore := settingstore.New(pool, cache)

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

	// 过滤规则从 DB（proxy_filter 组）读取为唯一事实源；读失败（如种子未导入）降级用 yaml，
	// 保证 proxy 能启动（首次导入种子后经失效总线热换到 DB 值）。
	filterCfg := proxyFilterConfig(ctx, settingStore, proxyCfg, logger)
	trafficFilter := filter.NewTrafficFilter(filterCfg)
	publisher, err := proxy.NewPublisher(rdb, proxyCfg.StreamName, int64(proxyCfg.StreamMaxLen))
	if err != nil {
		logger.Fatal().Err(err).Msg("new publisher")
	}

	// External（passive 入口）：proxify 监听 internal loopback，公开端口由 sanitizer 接管。
	externalServer, err := proxy.NewServer(proxy.ServerDeps{
		Filter:     trafficFilter,
		Publisher:  publisher,
		Cfg:        filterCfg, // body 上限属 proxy_filter 组，与过滤链同源（DB 优先）
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

	// 热换过滤链：proxy_filter 组失效（前端改「系统配置」后广播）→ 重读 DB 重建链原子换入。
	// 无脑触发、回调侧按键前缀过滤；重读走独立超时 ctx，失败仅 warn 保留旧链（不炸进程）。
	cache.OnInvalidate(func(_ context.Context, keys []string) {
		if !containsKey(keys, settingstore.KeyProxyFilter) {
			return
		}
		rctx, rcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer rcancel()
		newCfg := proxyFilterConfig(rctx, settingStore, proxyCfg, logger)
		externalServer.SwapFilter(filter.NewTrafficFilter(newCfg), newCfg.MaxRequestBodySize, newCfg.MaxResponseBodySize)
		logger.Info().Msg("proxy_filter 已热换（过滤链 + body 上限原子替换）")
	})
	// 失效总线订阅（阻塞循环，后台起）：收到广播即清本进程缓存并触发上面的热换回调。
	go func() {
		if err := cache.Subscribe(proxyCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("cachestore 失效订阅退出——过滤规则热换不可用")
		}
	}()

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

// proxyFilterConfig 把 proxy_filter 组（DB 事实源）叠加到 yaml ProxyConfig 的过滤字段上，
// 产出用于构建过滤链的 config.ProxyConfig。DB 为唯一事实源；读失败（种子未导入 / DB 抖动）
// 降级保留 yaml 值，保证 proxy 能启动，首次导入种子后经失效总线热换到 DB 值。
//
// 只覆盖过滤相关字段（黑白名单 + body 上限）；listen/stream 等基础设施字段仍取 yaml（不入 DB）。
func proxyFilterConfig(ctx context.Context, store *settingstore.Store, base config.ProxyConfig, logger zerolog.Logger) config.ProxyConfig {
	pf, err := store.ProxyFilter(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("读 proxy_filter 组失败，降级用 yaml 过滤规则")
		return base
	}
	base.AllowHosts = pf.AllowHosts
	base.ExcludeMethods = pf.ExcludeMethods
	base.ExcludeHosts = pf.ExcludeHosts
	base.ExcludeUpgradeProtocols = pf.ExcludeUpgradeProtocols
	base.ExcludeSuffixes = pf.ExcludeSuffixes
	base.ExcludeContentTypes = pf.ExcludeContentTypes
	base.ExcludeStatusCodes = pf.ExcludeStatusCodes
	base.MaxRequestBodySize = pf.MaxRequestBodySize
	base.MaxResponseBodySize = pf.MaxResponseBodySize
	return base
}

// containsKey 报告 keys 是否含目标键（失效回调按键前缀判本次失效是否与己相关）。
func containsKey(keys []string, target string) bool {
	for _, k := range keys {
		if k == target {
			return true
		}
	}
	return false
}
