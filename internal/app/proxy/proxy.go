// Package proxy 是 proxy 组件：内嵌 MITM 代理 + 过滤链 + Redis Stream 生产者。
//
// 只生产流量快照（XADD 到 Stream），不做存储：落库、按主机攒批建代理捕获任务、入队都在
// runner 的 ingestor。过滤规则的生效值 = 部署默认叠加前端「系统配置」的自定义，改后经
// 多级缓存的失效广播热换过滤链，因此本组件也连 PostgreSQL 并订阅失效总线。
//
// 单个公开监听：listen_addr → sanitizer（URI 修补）→ internal_addr 回环 → proxify。
// 沙盒自产流量不经本组件（浏览器经 CDP 抓取回灌 runner，CLI 工具直连不抓），避免
// agent 自己的流量回灌代理流量分析形成自激。存活检测探 listen_addr 的 TCP。
package proxy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/settingstore"
)

// Run 运行 proxy 组件直到 ctx 取消。启动失败返回错误。
func Run(ctx context.Context, cfg config.AppConfig) error {
	logger := logx.New("proxy")

	rdb, err := db.NewRedis(ctx, cfg.Redis)
	if err != nil {
		return fmt.Errorf("连接 Redis: %w", err)
	}
	defer func() { _ = rdb.Close() }()

	pool, err := db.NewPgPool(ctx, cfg.Postgres, logger)
	if err != nil {
		return fmt.Errorf("连接 PostgreSQL: %w", err)
	}
	defer pool.Close()

	cache := cachestore.New(rdb, 0)
	settingStore := settingstore.New(pool, cache, settingstore.DefaultsFrom(cfg))
	proxyCfg := cfg.Proxy

	filterCfg := proxyFilterConfig(ctx, settingStore, proxyCfg, logger)
	publisher, err := proxy.NewPublisher(rdb, proxyCfg.StreamName, int64(proxyCfg.StreamMaxLen))
	if err != nil {
		return fmt.Errorf("构造流量发布器: %w", err)
	}
	server, err := proxy.NewServer(proxy.ServerDeps{
		Filter:     filter.NewTrafficFilter(filterCfg),
		Publisher:  publisher,
		Cfg:        filterCfg, // body 上限属 proxy_filter 组，与过滤链同源
		ListenAddr: proxyCfg.InternalAddr,
		CertDir:    proxyCfg.CertDir,
		Source:     "external",
		Logger:     logger.With().Str("listener", "external").Logger(),
	})
	if err != nil {
		return fmt.Errorf("构造 MITM 代理: %w", err)
	}

	// 热换过滤链：proxy_filter 组失效（前端改「系统配置」后广播）→ 重读生效值、重建链原子换入。
	// 重读失败只告警、保留旧链。
	cache.OnInvalidate(func(_ context.Context, keys []string) {
		if !slices.Contains(keys, settingstore.KeyProxyFilter) {
			return
		}
		rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		next := proxyFilterConfig(rctx, settingStore, proxyCfg, logger)
		server.SwapFilter(filter.NewTrafficFilter(next), next.MaxRequestBodySize, next.MaxResponseBodySize)
		logger.Info().Msg("proxy_filter 已热换（过滤链 + body 上限原子替换）")
	})
	go func() {
		if err := cache.Subscribe(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("cachestore 失效订阅退出——过滤规则热换不可用")
		}
	}()

	proxyCtx, stopProxy := context.WithCancel(ctx)
	defer stopProxy()
	failed := make(chan error, 2)
	go func() {
		logger.Info().Str("loopback", proxyCfg.InternalAddr).Msg("mitm proxy starting")
		if err := server.Run(proxyCtx); err != nil && !errors.Is(err, context.Canceled) {
			failed <- fmt.Errorf("MITM 代理退出: %w", err)
		}
	}()
	go func() {
		logger.Info().Str("public", proxyCfg.ListenAddr).Str("upstream", proxyCfg.InternalAddr).Msg("uri sanitizer listening")
		if err := proxy.RunExternalSanitizer(proxyCtx, proxyCfg.ListenAddr, proxyCfg.InternalAddr); err != nil {
			failed <- fmt.Errorf("代理入口 %s 退出: %w", proxyCfg.ListenAddr, err)
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-failed:
	}
	server.Stop()
	logger.Info().Msg("proxy stopped")
	return runErr
}

// proxyFilterConfig 把 proxy_filter 组的生效值写入部署配置的过滤字段。读失败（DB 抖动）降级
// 保留部署默认，保证 proxy 能启动；之后任一次失效广播都会热换到生效值。
// 只覆盖过滤相关字段（黑白名单 + body 上限），监听与 Stream 等基础设施字段不入库。
func proxyFilterConfig(ctx context.Context, store *settingstore.Store, base config.ProxyConfig, logger zerolog.Logger) config.ProxyConfig {
	pf, err := store.ProxyFilter(ctx)
	if err != nil {
		logger.Warn().Err(err).Msg("读 proxy_filter 组失败，降级用部署默认过滤规则")
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
