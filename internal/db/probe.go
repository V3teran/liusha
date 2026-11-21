package db

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// PostgresStatus 是一次 PostgreSQL 探测的读数。
type PostgresStatus struct {
	Version        string        // 服务端版本（server_version）
	Latency        time.Duration // 一次往返（SELECT 1）的耗时
	Connections    int           // 数据库当前的客户端连接数（全部客户端，不只本进程）
	MaxConnections int           // 数据库的连接上限（max_connections）
}

// ProbePostgres 实测一次往返，并读出版本与数据库整体的连接占用——连接打满时所有组件都会
// 拿不到连接，这是数据库的饱和度。连不上或查询失败返回错误。
func ProbePostgres(ctx context.Context, pool *pgxpool.Pool) (PostgresStatus, error) {
	start := time.Now()
	var one int
	if err := pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		return PostgresStatus{}, fmt.Errorf("探测 PostgreSQL: %w", err)
	}
	st := PostgresStatus{Latency: time.Since(start)}
	err := pool.QueryRow(ctx, `
		SELECT current_setting('server_version'),
		       current_setting('max_connections')::int,
		       (SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend')`).
		Scan(&st.Version, &st.MaxConnections, &st.Connections)
	if err != nil {
		return PostgresStatus{}, fmt.Errorf("读 PostgreSQL 版本与连接数: %w", err)
	}
	// 「17.2 (Debian 17.2-1.pgdg120+1)」只留版本号
	st.Version, _, _ = strings.Cut(st.Version, " ")
	return st, nil
}

// RedisStatus 是一次 Redis 探测的读数。
type RedisStatus struct {
	Version    string        // 服务端版本（INFO server 的 redis_version）
	Latency    time.Duration // 一次 PING 的耗时
	UsedMemory int64         // 已用内存（字节）
	MaxMemory  int64         // 内存上限（字节）；0 = 未设上限
}

// ProbeRedis 实测一次 PING 并从 INFO 读出版本与内存。连不上或命令失败返回错误。
func ProbeRedis(ctx context.Context, rdb *redis.Client) (RedisStatus, error) {
	start := time.Now()
	if err := rdb.Ping(ctx).Err(); err != nil {
		return RedisStatus{}, fmt.Errorf("探测 Redis: %w", err)
	}
	st := RedisStatus{Latency: time.Since(start)}
	info, err := rdb.Info(ctx, "server", "memory").Result()
	if err != nil {
		return RedisStatus{}, fmt.Errorf("读 Redis INFO: %w", err)
	}
	fields := parseRedisInfo(info)
	st.Version = fields["redis_version"]
	st.UsedMemory, _ = strconv.ParseInt(fields["used_memory"], 10, 64)
	st.MaxMemory, _ = strconv.ParseInt(fields["maxmemory"], 10, 64)
	return st, nil
}

// parseRedisInfo 把 INFO 的「键:值」行解析成表（# 开头的是分节标题）。
func parseRedisInfo(info string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(info, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, ":"); ok {
			out[k] = v
		}
	}
	return out
}

// DataStores 是数据依赖的探测入口（概览的组件健康用）。
type DataStores struct {
	Pool  *pgxpool.Pool
	Redis *redis.Client
}

// Postgres 探测 PostgreSQL。
func (d DataStores) Postgres(ctx context.Context) (PostgresStatus, error) {
	return ProbePostgres(ctx, d.Pool)
}

// RedisStatus 探测 Redis。
func (d DataStores) RedisStatus(ctx context.Context) (RedisStatus, error) {
	return ProbeRedis(ctx, d.Redis)
}
