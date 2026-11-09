//go:build integration

// Package dbtest 提供共享的集成测试夹具：
// 启动 pgvector/pgvector:pg17 容器并按字典序应用全部 db/migrations/*.up.sql。
// 使用方式：在 *_integration_test.go 中调用 NewPgPool(t)。
package dbtest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// NewPgPool 启动一次性 Postgres 容器、应用全部迁移并返回 *pgxpool.Pool。
// 容器和连接池都通过 t.Cleanup 自动回收。
func NewPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newEmptyPool(t)
	applyMigrations(t, pool, func(string) bool { return true })
	return pool
}

// NewPgPoolBefore 建库并只应用文件名在 migration 之前的迁移（迁移测试用：先按旧结构
// 写入老数据，再用 ApplyMigrationsFrom 执行被测迁移）。
func NewPgPoolBefore(t *testing.T, migration string) *pgxpool.Pool {
	t.Helper()
	pool := newEmptyPool(t)
	applyMigrations(t, pool, func(name string) bool { return name < migration })
	return pool
}

// ApplyMigrationsFrom 应用文件名不早于 migration 的全部迁移。
func ApplyMigrationsFrom(t *testing.T, pool *pgxpool.Pool, migration string) {
	t.Helper()
	applyMigrations(t, pool, func(name string) bool { return name >= migration })
}

func newEmptyPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
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
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("解析 dsn 失败: %v", err)
	}
	// 与生产 db.NewPgPool 一致：每条连接注册 pgvector 类型（corpus.embedding 扫描用）。
	// 注册失败不致命——迁移建 vector 扩展前的连接仍可用（降级文本协议），对齐生产语义。
	cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_ = pgxvec.RegisterTypes(ctx, conn)
		return nil
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("创建 pgxpool 失败: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// applyMigrations 按字典序依次执行 db/migrations/*.up.sql 中被 include 选中的文件。
func applyMigrations(t *testing.T, pool *pgxpool.Pool, include func(name string) bool) {
	t.Helper()
	ctx := context.Background()
	files, err := filepath.Glob(filepath.Join(repoPath("db/migrations"), "*.up.sql"))
	if err != nil {
		t.Fatalf("glob 迁移文件失败: %v", err)
	}
	sort.Strings(files)
	for _, f := range files {
		if !include(filepath.Base(f)) {
			continue
		}
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", f, err)
		}
		if _, err := pool.Exec(ctx, string(b)); err != nil {
			t.Fatalf("应用 %s 失败: %v", filepath.Base(f), err)
		}
	}
}

// repoPath 解析仓库相对路径（从本文件所在目录回退两级到 repo root）。
func repoPath(rel string) string {
	_, file, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", rel)
}

// SeedAssignment 插入一条最小 assignment 并返回其 id，供需要 task 外键归属的测试复用。
// ID 传待建 task 所属场景 code，与 task 的 _id 保持一致即可。
func SeedAssignment(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var id string
	err := pool.QueryRow(context.Background(),
		`INSERT INTO assignment (source, payload, title)
		 VALUES ('direct', '[]'::jsonb, 'test') RETURNING id`).Scan(&id)
	if err != nil {
		t.Fatalf("seed assignment: %v", err)
	}
	return id
}
