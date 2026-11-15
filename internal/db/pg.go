// Package db 封装 PostgreSQL / Redis 连接构造：统一 DSN 解析、连接池参数与
// 生产部署约定（pgvector 类型注册等），供全部需要持久层的入口复用。
package db

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/tracelog"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/logx"
)

// NewPgPool 构造 pgxpool.Pool 并 Ping 验证。
//
// 连接池参数取自 cfg：maxConns / connectTimeout / maxConnLifetime ≤ 0 时不覆盖 pgxpool 默认，
// minConns 为 0 时保底 2 条暖连接（见下）。
func NewPgPool(ctx context.Context, cfg config.PostgresConfig, logger zerolog.Logger) (*pgxpool.Pool, error) {
	if cfg.DSN == "" {
		return nil, errors.New("postgres.dsn 未配置（环境变量 LIUSHA_POSTGRES_DSN）")
	}
	pcfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse pg dsn: %w", err)
	}
	maxConns, minConns := cfg.MaxConns, cfg.MinConns
	connectTimeoutSec, maxConnLifetimeSec := cfg.ConnectTimeoutSeconds, cfg.MaxConnLifetimeSeconds
	// 池级日志接进 logx（pgx tracelog 适配）：连接失效/获取超时等池事件调用方
	// 看不见，此前完全不可见。只透传 warn+——info 级是每查询一条的噪声；
	// 查询级错误本就由各 store 调用方记录，这里兜的是池/连接内部事件。
	pcfg.ConnConfig.Tracer = &tracelog.TraceLog{
		Logger:   pgxLogger{log: logger},
		LogLevel: tracelog.LogLevelWarn,
	}
	if maxConns > 0 && maxConns <= math.MaxInt32 {
		pcfg.MaxConns = int32(maxConns)
	}
	// 暖连接兜底（调用方未配时）：MinConns 默认 0 会在空闲时清空池，闲置后的
	// 首个请求付一次全新拨号——macOS Docker 端口转发对新拨号会间歇性卡顿
	// （实测 api 单日 38 次 dial error，用户登录直接撞上）。保底 2 条常驻
	// 连接让请求路径复用暖连接，新拨号只发生在池自身的健康检查里。
	if minConns == 0 {
		minConns = 2
	}
	if minConns > 0 && minConns <= math.MaxInt32 {
		pcfg.MinConns = int32(minConns)
	}
	// 拨号超时兜底 5s：既防 vpnkit 半开连接无限挂起，也不至于长时间占住请求 ctx。
	if connectTimeoutSec == 0 {
		pcfg.ConnConfig.ConnectTimeout = 5 * time.Second
	} else if connectTimeoutSec > 0 {
		pcfg.ConnConfig.ConnectTimeout = time.Duration(connectTimeoutSec) * time.Second
	}
	if maxConnLifetimeSec > 0 {
		pcfg.MaxConnLifetime = time.Duration(maxConnLifetimeSec) * time.Second
	}
	// 每条连接注册 pgvector 类型，启用 corpus.embedding vector 列的二进制编解码。
	// 注册失败不致命（返回 nil）：vector 扩展尚未建立时（全新库、迁移先于建池的引导窗口）
	// 连接仍应可用——pgvector.Vector 实现 driver.Valuer/sql.Scanner，缺注册时自动降级文本协议，
	// vector 操作照常工作，只是少了二进制优化。不因类型注册失败阻断整个连接池。
	pcfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_ = pgxvec.RegisterTypes(ctx, conn)
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, pcfg)
	if err != nil {
		return nil, fmt.Errorf("new pgxpool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping pg: %w", err)
	}
	return pool, nil
}

// pgxLogger 把 pgx tracelog 的事件桥接到 logx。pgx 的字段键与本日志器的
// 保留键冲突，桥接时归一：err 抬到 zerolog error 字段（错误统一走 error
// 通道，下游解析与展示把它当一等公民）；time 是操作耗时（ms），与时间戳
// 键同名——不改名 JSON 行会出现重复键，解析取后写者导致耗时被静默吞掉，
// 改名 elapsed_ms。
type pgxLogger struct{ log zerolog.Logger }

func (l pgxLogger) Log(_ context.Context, level tracelog.LogLevel, msg string, data map[string]any) {
	ev := l.log.Warn()
	if level == tracelog.LogLevelError {
		// 调用方上下文取消（任务终止、进程停止）导致的查询失败是正常收尾，不是数据库故障。
		cause, _ := data["err"].(error)
		ev = logx.ErrorUnlessCanceled(l.log, cause)
	}
	fields := make(map[string]any, len(data))
	for k, v := range data {
		switch k {
		case "err":
			if e, ok := v.(error); ok {
				ev = ev.AnErr("error", e)
				continue
			}
			fields["error"] = v
		case "time":
			fields["elapsed_ms"] = v
		default:
			fields[k] = v
		}
	}
	ev.Fields(fields).Msg(msg)
}
