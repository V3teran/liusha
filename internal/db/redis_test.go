package db

import (
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/config"
)

// 连接串承载地址、用户名、密码、库号与 TLS；连接池与超时取自配置的其余键，原样生效。
func TestRedisOptions(t *testing.T) {
	cfg := config.RedisConfig{
		URL:      "rediss://app:secret@cache.internal:6380/3",
		PoolSize: 20, MinIdleConns: 2, DialTimeoutSeconds: 5, ReadTimeoutSeconds: 3, WriteTimeoutSeconds: 4,
	}
	o, err := RedisOptions(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if o.Addr != "cache.internal:6380" || o.Username != "app" || o.Password != "secret" || o.DB != 3 || o.TLSConfig == nil {
		t.Fatalf("连接串解析不符: addr=%q user=%q db=%d tls=%v", o.Addr, o.Username, o.DB, o.TLSConfig != nil)
	}
	if o.PoolSize != 20 || o.MinIdleConns != 2 || o.DialTimeout != 5*time.Second ||
		o.ReadTimeout != 3*time.Second || o.WriteTimeout != 4*time.Second {
		t.Fatalf("连接池与超时未按配置生效: %+v", o)
	}
}

func TestRedisOptions_Rejects(t *testing.T) {
	for _, u := range []string{"", "127.0.0.1:6379", "http://h:6379"} {
		if _, err := RedisOptions(config.RedisConfig{URL: u}); err == nil {
			t.Fatalf("redis.url=%q 应报错", u)
		}
	}
}
