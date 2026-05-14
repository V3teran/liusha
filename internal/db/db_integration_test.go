//go:build integration

package db

import (
	"context"
	"strings"
	"testing"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	tcredis "github.com/testcontainers/testcontainers-go/modules/redis"

	"github.com/V3teran/liusha/internal/config"
)

func TestNewPgPool_Ping(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	c, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("liusha"),
		tcpostgres.WithUsername("liusha"),
		tcpostgres.WithPassword("liusha"),
		tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	pool, err := NewPgPool(ctx, dsn, 5, 1, 5, 0)
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
	r, err := NewRedis(ctx, strings.TrimPrefix(addr, "redis://"), config.RedisConfig{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.Ping(ctx).Err(); err != nil {
		t.Fatalf("ping: %v", err)
	}
}
