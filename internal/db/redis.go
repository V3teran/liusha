package db

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/config"
)

// NewRedis 构造 redis client 并 Ping 验证。
//
// addr 来自 ENV LIUSHA_REDIS_ADDR；cfg 来自 yaml redis: 节，任一字段为 0 时
// 透传到 go-redis 让其用 SDK 内部默认（PoolSize=10×GOMAXPROCS / DialTimeout=5s /
// ReadTimeout=3s / WriteTimeout=3s）。
func NewRedis(ctx context.Context, addr string, cfg config.RedisConfig) (*redis.Client, error) {
	opts := &redis.Options{Addr: addr}
	if cfg.PoolSize > 0 {
		opts.PoolSize = cfg.PoolSize
	}
	if cfg.MinIdleConns > 0 {
		opts.MinIdleConns = cfg.MinIdleConns
	}
	if cfg.DialTimeoutSeconds > 0 {
		opts.DialTimeout = time.Duration(cfg.DialTimeoutSeconds) * time.Second
	}
	if cfg.ReadTimeoutSeconds > 0 {
		opts.ReadTimeout = time.Duration(cfg.ReadTimeoutSeconds) * time.Second
	}
	if cfg.WriteTimeoutSeconds > 0 {
		opts.WriteTimeout = time.Duration(cfg.WriteTimeoutSeconds) * time.Second
	}
	c := redis.NewClient(opts)
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping redis %s: %w", addr, err)
	}
	return c, nil
}
