package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/cryptx"
	"github.com/V3teran/liusha/internal/finding"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/llmstore"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/scanstream"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

var initLog = logx.New("api.init")

// apiConfig 封装 API 所需的全部配置。
type apiConfig struct {
	pgDSN       string
	redisDSN    string
	listenAddr  string
	corsOrigin  string
	runTimeout  time.Duration
	llmKeySecret string // LLM provider API Key 加密密钥
	logLevel    string
}

// loadConfig 从环境变量加载配置。
func loadConfig() apiConfig {
	cfg := apiConfig{
		pgDSN:        getEnvOrDefault("POSTGRES_DSN", "postgres://liusha:liusha@localhost:5432/liusha?sslmode=disable"),
		redisDSN:     getEnvOrDefault("REDIS_DSN", "redis://localhost:6379/0"),
		listenAddr:   getEnvOrDefault("API_LISTEN_ADDR", ":8090"),
		corsOrigin:   getEnvOrDefault("CORS_ORIGIN", "http://localhost:5173"),
		runTimeout:   parseDurationOrDefault(getEnvOrDefault("AGENT_RUN_TIMEOUT", "4h"), 4*time.Hour),
		llmKeySecret: os.Getenv("LIUSHA_LLM_KEY_SECRET"),
		logLevel:     getEnvOrDefault("LOG_LEVEL", "info"),
	}
	return cfg
}

// dependencies 封装所有依赖（stores、clients、router）。
type dependencies struct {
	pg            *pgxpool.Pool
	rdb           *redis.Client
	router        *fwllm.Router
	worker        *worker.Client
	publisher     *scanstream.Publisher
	assignments   *assignment.Store
	tasks         *task.Store
	agentRuns     *agentrun.Store
	conversations *conversation.Store
	findings      *finding.Store
	proxyStore    *traffic.ProxyStore
	agentStore    *traffic.AgentStore
	audit         *audit.Store
}

// initDependencies 初始化所有依赖。
func initDependencies(ctx context.Context, cfg apiConfig) (*dependencies, error) {
	// PostgreSQL
	pg, err := pgxpool.New(ctx, cfg.pgDSN)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}
	if err := pg.Ping(ctx); err != nil {
		pg.Close()
		return nil, fmt.Errorf("ping postgres: %w", err)
	}
	initLog.Info().Msg("PostgreSQL 连接成功")

	// Redis
	opts, err := redis.ParseURL(cfg.redisDSN)
	if err != nil {
		pg.Close()
		return nil, fmt.Errorf("parse redis dsn: %w", err)
	}
	rdb := redis.NewClient(opts)
	if err := rdb.Ping(ctx).Err(); err != nil {
		pg.Close()
		rdb.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	initLog.Info().Msg("Redis 连接成功")

	// LLM Key 解密密钥（fail-fast 校验）
	keyCipher, err := cryptx.NewFromEnv("LIUSHA_LLM_KEY_SECRET")
	if err != nil {
		pg.Close()
		rdb.Close()
		return nil, fmt.Errorf("LIUSHA_LLM_KEY_SECRET 未配置或不合法（provider 密钥解密需要它）: %w", err)
	}

	// LLM Store + Router（与 runner 同构）
	cache := cachestore.New(rdb, 5*time.Minute)
	llmStore := llmstore.New(pg, cache)
	router := fwllm.NewRouterWithFallback(
		llmStore.AsRouterStore(keyCipher),
		newFallbackProviderFactory(llmStore, keyCipher),
	)
	initLog.Info().Msg("LLM Router 初始化成功")

	// Worker Client
	workerClient := worker.NewClient(asynq.RedisClientOpt{Addr: cfg.redisDSN})
	initLog.Info().Msg("Worker Client 初始化成功")
	initLog.Info().Msg("Worker Client 初始化成功")

	// SSE Publisher
	publisher := scanstream.NewPublisher(rdb)

	// Stores
	deps := &dependencies{
		pg:            pg,
		rdb:           rdb,
		router:        router,
		worker:        workerClient,
		publisher:     publisher,
		assignments:   assignment.NewStore(pg),
		tasks:         task.NewStore(pg),
		agentRuns:     agentrun.NewStore(pg),
		conversations: conversation.NewStore(pg),
		findings:      finding.NewStore(pg),
		proxyStore:    traffic.NewProxyStore(pg),
		agentStore:    traffic.NewAgentStore(pg),
		audit:         audit.NewStore(pg),
	}

	initLog.Info().Msg("所有依赖初始化完成")
	return deps, nil
}

// cleanup 清理资源。
func cleanup(deps *dependencies) {
	if deps == nil {
		return
	}
	if deps.pg != nil {
		deps.pg.Close()
		initLog.Info().Msg("PostgreSQL 连接已关闭")
	}
	if deps.rdb != nil {
		_ = deps.rdb.Close()
		initLog.Info().Msg("Redis 连接已关闭")
	}
	if deps.worker != nil {
		_ = deps.worker.Close()
		initLog.Info().Msg("Worker Client 已关闭")
	}
}

// getEnvOrDefault 获取环境变量，若不存在则返回默认值。
func getEnvOrDefault(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

// parseDurationOrDefault 解析时长字符串，失败时返回默认值。
func parseDurationOrDefault(s string, defaultValue time.Duration) time.Duration {
	d, err := time.ParseDuration(s)
	if err != nil {
		return defaultValue
	}
	return d
}
