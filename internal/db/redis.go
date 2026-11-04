package db

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/config"
)

// NewRedis 构造 redis client 并 Ping 验证。
//
// addr 来自 ENV LIUSHA_REDIS_ADDR；cfg 来自 yaml redis: 节，任一数值字段为 0 时
// 透传到 go-redis 让其用 SDK 内部默认（PoolSize=10×GOMAXPROCS / DialTimeout=5s /
// ReadTimeout=3s / WriteTimeout=3s）。
//
// 密码：ENV LIUSHA_REDIS_PASSWORD 优先于 yaml redis.password——env 覆盖在此
// 显式读取而非依赖 viper AutomaticEnv（其对 yaml 未声明的键静默不生效，
// 自定义 config.yaml 省略 redis 节时会导致密码丢失、启动报 NOAUTH）。
func NewRedis(ctx context.Context, addr string, cfg config.RedisConfig) (*redis.Client, error) {
	password := cfg.Password
	if v := os.Getenv("LIUSHA_REDIS_PASSWORD"); v != "" {
		password = v
	}
	opts := &redis.Options{Addr: addr, Password: password}
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
