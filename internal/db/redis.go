package db

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/config"
)

// RedisOptions 把配置换算为 go-redis 连接选项：连接串（地址、用户名、密码、库号、TLS）取自
// redis.url，连接池与超时取自配置的其余键。任务队列（asynq）的客户端也从这里取选项，两者连到
// 同一个 Redis、同一个库。
func RedisOptions(cfg config.RedisConfig) (*redis.Options, error) {
	if cfg.URL == "" {
		return nil, errors.New("redis.url 未配置（环境变量 LIUSHA_REDIS_URL）")
	}
	opts, err := redis.ParseURL(cfg.URL)
	if err != nil {
		return nil, fmt.Errorf("解析 redis.url: %w", err)
	}
	opts.PoolSize = cfg.PoolSize
	opts.MinIdleConns = cfg.MinIdleConns
	opts.DialTimeout = time.Duration(cfg.DialTimeoutSeconds) * time.Second
	opts.ReadTimeout = time.Duration(cfg.ReadTimeoutSeconds) * time.Second
	opts.WriteTimeout = time.Duration(cfg.WriteTimeoutSeconds) * time.Second
	return opts, nil
}

// NewRedis 构造 redis client 并 Ping 验证。
func NewRedis(ctx context.Context, cfg config.RedisConfig) (*redis.Client, error) {
	opts, err := RedisOptions(cfg)
	if err != nil {
		return nil, err
	}
	c := redis.NewClient(opts)
	if err := c.Ping(ctx).Err(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("ping redis %s: %w", opts.Addr, err)
	}
	return c, nil
}
