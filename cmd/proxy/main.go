// Package main 是 liusha proxy 进程入口：内嵌 proxify SDK 的 MITM 代理 +
// filter 责任链 + 去重 + aggregator + Asynq 生产者。
//
// 进程职责（业界最佳实践——proxy 与 worker 解耦，故障隔离 + 独立扩缩）：
//  1. config.Load + db.NewPgPool + db.NewRedis 启动依赖
//  2. 构造 stores：engagement / flow / window
//  3. worker.Client（生产者侧，把 sniffer 任务入队 Asynq）
//  4. filter.NewTrafficFilter / proxy.NewTrafficDeduplicator / proxy.NewAggregator
//  5. proxyFlush sink：snapshot → engagement.LookupOrCreate → flow.Append → window.OpenOrAppend → enqueue sniffer
//  6. proxy.NewServer + Run（监听 LIUSHA_PROXY_LISTEN_ADDR，默认 0.0.0.0:8888）
//  7. healthz HTTP（默认 :9091，与 agent-worker :9090 错开）
//  8. graceful shutdown（SIGINT/SIGTERM → proxyServer.Stop + aggregator.Stop + 关 db/redis）
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
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/filter"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/window"
	"github.com/V3teran/liusha/internal/worker"

	"github.com/hibiken/asynq"
)

// shutdownTimeout 是 healthz HTTP 优雅关闭的超时。
const shutdownTimeout = 5 * time.Second

// flow body 截断阈值——与 agent-worker 对齐，远超 spec §proxify 32 KiB，让 BAC replay 拿全 body。
const (
	flowMaxRequestBody  = 1 << 20
	flowMaxResponseBody = 2 << 20
)

// dedupeWindow 是 TrafficDeduplicator 内部 hash 过期时间。
const dedupeWindow = 60 * time.Second

func main() {
	logger := logx.New("proxy")
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

	redisAddr := os.Getenv("LIUSHA_REDIS_ADDR")
	rdb, err := db.NewRedis(ctx, redisAddr)
	if err != nil {
		logger.Fatal().Err(err).Msg("redis")
	}
	defer rdb.Close()

	// Stores：proxy 进程仅写 engagement/flow/window 三张表，其它表归 agent-worker。
	engs := engagement.NewStore(pool)
	flows := flow.NewStore(pool, flowMaxRequestBody, flowMaxResponseBody)
	windows := window.NewStore(pool)

	// Asynq 生产者：把 window-closed 投递为 sniffer 任务，与 agent-worker（消费者）共用 redis。
	wc := worker.NewClient(asynq.RedisClientOpt{Addr: redisAddr})
	defer wc.Close()

	// 内嵌 MITM 代理装配链：filter → aggregator(dedup, sink) → proxy.Server
	listenAddr := envOr("LIUSHA_PROXY_LISTEN_ADDR", "0.0.0.0:8888")
	certDir := envOr("LIUSHA_PROXY_CERT_DIR", "") // 空则 proxy.Server 内部默认 $HOME/.liusha
	proxyCfg := cfg.Proxy

	trafficFilter := filter.NewTrafficFilter(proxyCfg)
	dedup := proxy.NewTrafficDeduplicator()
	sink := newProxyFlush(engs, flows, windows, wc, proxyCfg, logger)

	aggregator := proxy.NewAggregator(
		time.Duration(proxyCfg.WindowMaxAgeSeconds)*time.Second,
		dedupeWindow,
		proxyCfg.WindowBatch,
		dedup,
		sink,
	)
	proxyCtx, proxyCancel := context.WithCancel(context.Background())
	defer proxyCancel()
	aggregator.Start(proxyCtx)

	proxyServer, err := proxy.NewServer(proxy.ServerDeps{
		Filter:     trafficFilter,
		Aggregator: aggregator,
		Cfg:        proxyCfg,
		ListenAddr: listenAddr,
		CertDir:    certDir,
		Logger:     logger,
	})
	if err != nil {
		logger.Fatal().Err(err).Msg("new mitm proxy")
	}

	// healthz HTTP：默认 :9091，避免与 agent-worker :9090 冲突。
	hsAddr := envOr("LIUSHA_PROXY_HEALTHZ_ADDR", ":9091")
	hsMux := http.NewServeMux()
	hsMux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	hs := &http.Server{Addr: hsAddr, Handler: hsMux, ReadHeaderTimeout: 5 * time.Second}

	go func() {
		logger.Info().Str("addr", hs.Addr).Msg("proxy healthz listening")
		if err := hs.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error().Err(err).Msg("healthz serve")
		}
	}()

	go func() {
		logger.Info().Str("addr", listenAddr).Msg("mitm proxy starting")
		if err := proxyServer.Run(proxyCtx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error().Err(err).Msg("mitm proxy exited")
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	sig := <-stop
	logger.Info().Str("signal", sig.String()).Msg("proxy shutting down")

	// 关停顺序：先停 mitm（不再产生 snapshot）→ aggregator flush 残留 → healthz。
	proxyServer.Stop()
	proxyCancel()
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
