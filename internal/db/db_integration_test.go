//go:build integration

package db

import (
	"context"
	"strings"
	"testing"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/containertest"
)

// startPostgres 启动一次性 pgvector 容器，返回 DSN。
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	c, err := containertest.Start(ctx, func(ctx context.Context) (*tcpostgres.PostgresContainer, error) {
		return tcpostgres.Run(ctx, "pgvector/pgvector:pg17",
			tcpostgres.WithDatabase("liusha"),
			tcpostgres.WithUsername("liusha"),
			tcpostgres.WithPassword("liusha"),
			tcpostgres.BasicWaitStrategies())
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	return dsn
}

func TestNewPgPool_Ping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	dsn := startPostgres(t)
	pool, err := NewPgPool(ctx, config.PostgresConfig{URL: dsn, MaxConns: 5, MinConns: 1, ConnectTimeoutSeconds: 5, MaxConnLifetimeSeconds: 3600}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
	st, err := ProbePostgres(ctx, pool)
	if err != nil {
		t.Fatalf("探测: %v", err)
	}
	if st.Version == "" || strings.Contains(st.Version, " ") || st.Latency <= 0 || st.Connections < 1 || st.MaxConnections < st.Connections {
		t.Fatalf("探测读数不符: %+v", st)
	}
}

func TestNewRedis_Ping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := containertest.Start(ctx, func(ctx context.Context) (*tcredis.RedisContainer, error) { return tcredis.Run(ctx, "redis:8") })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	addr, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRedis(ctx, config.RedisConfig{URL: addr, PoolSize: 5, DialTimeoutSeconds: 5, ReadTimeoutSeconds: 3, WriteTimeoutSeconds: 3})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
	st, err := ProbeRedis(ctx, r)
	if err != nil {
		t.Fatalf("探测: %v", err)
	}
	if !strings.HasPrefix(st.Version, "8.") || st.Latency <= 0 || st.UsedMemory <= 0 {
		t.Fatalf("探测读数不符: %+v", st)
	}
}
