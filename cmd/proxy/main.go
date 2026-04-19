// Package main 启动 liusha-proxy：运行 Proxify 子进程并将采集到的流量
// 经 ScopePolicy 过滤后写入 Redis 热层和 Postgres 冷层。
package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/config"
	engstore "github.com/V3teran/liusha/internal/engagement/store"
	flowstore "github.com/V3teran/liusha/internal/flow/store"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/proxy"
	"github.com/V3teran/liusha/internal/scope"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

func main() {
	cfgPath := os.Getenv("LIUSHA_CONFIG")
	if cfgPath == "" {
		cfgPath = "config/config.yaml"
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		panic("加载配置失败: " + err.Error())
	}
	logx.Init(cfg.LogLevel, cfg.Env == "development")
	bootCtx := logx.WithTraceID(context.Background(), logx.NewTraceID())

	engagementIDStr := os.Getenv("LIUSHA_ENGAGEMENT_ID")
	if engagementIDStr == "" {
		logx.From(bootCtx).Fatal().Msg("必须设置 LIUSHA_ENGAGEMENT_ID 环境变量")
	}
	engID, err := uuid.Parse(engagementIDStr)
	if err != nil {
		logx.From(bootCtx).Fatal().Err(err).Msg("LIUSHA_ENGAGEMENT_ID 不是合法 UUID")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	pool, err := pgxpool.New(ctx, cfg.PostgresDSN)
	if err != nil {
		logx.From(bootCtx).Fatal().Err(err).Msg("连接 postgres 失败")
	}
	defer pool.Close()

	// 从 Postgres 加载 engagement scope
	engSt := engstore.NewPostgres(pool)
	eng, err := engSt.Get(ctx, engID)
	if err != nil {
		logx.From(bootCtx).Fatal().Err(err).Str("engagement_id", engID.String()).Msg("加载 engagement 失败")
	}

	policy, err := buildPolicy(eng.Scope)
	if err != nil {
		logx.From(bootCtx).Fatal().Err(err).Msg("构建 ScopePolicy 失败")
	}

	rdb := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr})
	defer rdb.Close()

	flushDur := time.Duration(cfg.Proxy.FlushSeconds) * time.Second
	if flushDur <= 0 {
		flushDur = 5 * time.Second
	}
	batchSize := cfg.Proxy.BatchSize
	if batchSize <= 0 {
		batchSize = 50
	}

	ing := proxy.NewIngestor(policy, flowstore.NewRedis(rdb), flowstore.NewPostgres(pool), proxy.IngestConfig{
		BatchSize:     batchSize,
		FlushInterval: flushDur,
	})

	ch := make(chan proxy.ParsedFlow, 512)
	go proxy.TailJSONL(ctx, cfg.Proxy.FlowFile, engID, ch)
	go ing.Run(ctx, ch)

	// 启动 proxify 子进程
	proxyAddr := cfg.Proxy.Addr
	if proxyAddr == "" {
		proxyAddr = "0.0.0.0:8888"
	}
	proxifyCmd := exec.CommandContext(ctx, "proxify",
		"-ha", proxyAddr,
		"-of", "jsonl",
		"-o", cfg.Proxy.FlowFile,
		"-duc",
		"-silent",
	)
	proxifyCmd.Stdout = os.Stdout
	proxifyCmd.Stderr = os.Stderr
	if err := proxifyCmd.Start(); err != nil {
		logx.From(bootCtx).Fatal().Err(err).Msg("启动 proxify 失败")
	}
	logx.From(bootCtx).Info().Str("addr", proxyAddr).Msg("proxify 启动")

	<-ctx.Done()
	logx.From(bootCtx).Info().Msg("收到关停信号，等待 proxify 退出")
	_ = proxifyCmd.Wait()
}

// buildPolicy 从 engagement.Scope (map[string]any) 反序列化并构造 ScopePolicy。
func buildPolicy(s map[string]any) (*scope.Policy, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, err
	}
	var rules scope.Rules
	if err := json.Unmarshal(b, &rules); err != nil {
		return nil, err
	}
	return scope.NewPolicy(rules)
}
