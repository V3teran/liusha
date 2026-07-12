package db

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
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
	// 每条连接注册 pgvector 类型，启用 corpus.embedding vector 列的二进制编解码。
	// 注册失败不致命（返回 nil）：vector 扩展尚未建立时（全新库、迁移先于建池的引导窗口）
	// 连接仍应可用——pgvector.Vector 实现 driver.Valuer/sql.Scanner，缺注册时自动降级文本协议，
	// vector 操作照常工作，只是少了二进制优化。不因类型注册失败阻断整个连接池。
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_ = pgxvec.RegisterTypes(ctx, conn)
		return nil
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
