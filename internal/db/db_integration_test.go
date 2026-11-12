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
)

// startPostgres 启动一次性 pgvector 容器，返回 DSN。
func startPostgres(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	t.Cleanup(cancel)
	c, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("liusha"),
		tcpostgres.WithUsername("liusha"),
		tcpostgres.WithPassword("liusha"),
		tcpostgres.BasicWaitStrategies())
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
	pool, err := NewPgPool(ctx, config.PostgresConfig{DSN: dsn, MaxConns: 5, MinConns: 1, ConnectTimeoutSeconds: 5}, zerolog.Nop())
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestNewRedis_Ping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c, err := tcredis.Run(ctx, "redis:8")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	addr, err := c.ConnectionString(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRedis(ctx, config.RedisConfig{Addr: strings.TrimPrefix(addr, "redis://")})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}
