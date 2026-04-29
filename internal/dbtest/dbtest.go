//go:build integration

// Package dbtest 提供共享的集成测试夹具：
// 启动 pgvector/pgvector:pg17 容器并自动应用 0001_init.up.sql。
// 使用方式：在 *_integration_test.go 中调用 NewPgPool(t)。
package dbtest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// NewPgPool 启动一次性 Postgres 容器、应用迁移并返回 *pgxpool.Pool。
// 容器和连接池都通过 t.Cleanup 自动回收。
func NewPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	c, err := tcpostgres.Run(ctx, "pgvector/pgvector:pg17",
		tcpostgres.WithDatabase("liusha"),
		tcpostgres.WithUsername("liusha"),
		tcpostgres.WithPassword("liusha"),
		tcpostgres.BasicWaitStrategies())
	if err != nil {
		t.Fatalf("启动 postgres 容器失败: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(ctx) })

	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("获取 dsn 失败: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("创建 pgxpool 失败: %v", err)
	}
	t.Cleanup(pool.Close)

	sqlBytes, err := os.ReadFile(repoPath("db/migrations/0001_init.up.sql"))
	if err != nil {
		t.Fatalf("读取迁移文件失败: %v", err)
	}
	if _, err := pool.Exec(ctx, string(sqlBytes)); err != nil {
		t.Fatalf("应用迁移失败: %v", err)
	}
	return pool
}

// repoPath 解析仓库相对路径（从本文件所在目录回退两级到 repo root）。
func repoPath(rel string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", rel)
}
