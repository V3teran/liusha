package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// NewPgPool 构造 pgxpool.Pool 并 Ping 验证。
//
//	dsn:                 PG DSN（"postgres://user:pass@host/db"）
//	maxConns / minConns: 连接池上下限；≤ 0 时不覆盖 pgxpool 默认
//	connectTimeoutSec:   单次连接握手超时；≤ 0 时不覆盖 pgxpool 默认
//	maxConnLifetimeSec:  连接最大存活时间；≤ 0 时不覆盖 pgxpool 默认
//
// 调用方通常从 cfg.Postgres 注入全部 4 个参数；本函数零值跳过覆盖，让 pgx 自身默认生效。
func NewPgPool(ctx context.Context, dsn string, maxConns, minConns, connectTimeoutSec, maxConnLifetimeSec int) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}
	if maxConns > 0 {
		cfg.MaxConns = int32(maxConns)
	}
	if minConns >= 0 {
		cfg.MinConns = int32(minConns)
	}
	if connectTimeoutSec > 0 {
		cfg.ConnConfig.ConnectTimeout = time.Duration(connectTimeoutSec) * time.Second
	}
	if maxConnLifetimeSec > 0 {
		cfg.MaxConnLifetime = time.Duration(maxConnLifetimeSec) * time.Second
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("new pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping pg: %w", err)
	}
	return pool, nil
}
