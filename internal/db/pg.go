// Package db 封装 PostgreSQL / Redis 连接构造：统一 DSN 解析、连接池参数与
// 生产部署约定（pgvector 类型注册等），供全部需要持久层的入口复用。
package db

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/rs/zerolog"
)

// NewPgPool 构造 pgxpool.Pool 并 Ping 验证。
//
//	dsn:                 PG DSN（"postgres://user:pass@host/db"）
//	maxConns / minConns: 连接池上下限；≤ 0 时不覆盖 pgxpool 默认
//	connectTimeoutSec:   单次连接握手超时；≤ 0 时不覆盖 pgxpool 默认
//	maxConnLifetimeSec:  连接最大存活时间；≤ 0 时不覆盖 pgxpool 默认
//
// 调用方通常从 cfg.Postgres 注入全部 4 个参数；本函数零值跳过覆盖，让 pgx 自身默认生效。
func NewPgPool(ctx context.Context, dsn string, logger zerolog.Logger, maxConns, minConns, connectTimeoutSec, maxConnLifetimeSec int) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}
	// 池级日志接进 logx（pgx tracelog 适配）：连接失效/获取超时等池事件调用方
	// 看不见，此前完全不可见。只透传 warn+——info 级是每查询一条的噪声；
	// 查询级错误本就由各 store 调用方记录，这里兜的是池/连接内部事件。
	cfg.ConnConfig.Tracer = &tracelog.TraceLog{
		Logger:   pgxLogger{log: logger},
		LogLevel: tracelog.LogLevelWarn,
	}
	if maxConns > 0 {
		if maxConns > 0 && maxConns <= math.MaxInt32 {
			cfg.MaxConns = int32(maxConns)
		}
	}
	if minConns >= 0 {
		if minConns >= 0 && minConns <= math.MaxInt32 {
			cfg.MinConns = int32(minConns)
		}
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

// pgxLogger 把 pgx tracelog 的事件桥接到 logx。data 里是字段级上下文
// （pid/err 等），原样平铺进结构化字段；trace/debug 在 TraceLog 层已被级别过滤。
type pgxLogger struct{ log zerolog.Logger }

func (l pgxLogger) Log(_ context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	switch level {
	case tracelog.LogLevelError:
		l.log.Error().Fields(data).Msg(msg)
	case tracelog.LogLevelWarn:
		l.log.Warn().Fields(data).Msg(msg)
	}
}
