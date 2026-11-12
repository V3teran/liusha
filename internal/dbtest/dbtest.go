//go:build integration

// Package dbtest 提供共享的集成测试夹具：
// 启动 pgvector/pgvector:pg17 容器，用生产同一个迁移器（db.Migrator + 内置迁移）建库。
// 使用方式：在 *_integration_test.go 中调用 NewPgPool(t)。
package dbtest

import (
	"context"
	"io/fs"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/V3teran/liusha/db/migrations"
	"github.com/V3teran/liusha/internal/db"
)

// NewPgPool 启动一次性 Postgres 容器、迁移到最新版本并返回 *pgxpool.Pool。
// 容器和连接池都通过 t.Cleanup 自动回收。
func NewPgPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	pool := newEmptyPool(t)
	MigrateUp(t, pool)
	return pool
}

// NewPgPoolBefore 建库并只迁移到 version 之前的最后一个版本（迁移测试用：先按旧结构
// 写入老数据，再用 MigrateUp 执行被测迁移）。
func NewPgPoolBefore(t *testing.T, version uint) *pgxpool.Pool {
	t.Helper()
	pool := newEmptyPool(t)
	if prev := previousVersion(t, version); prev > 0 {
		withMigrator(t, pool, func(m *db.Migrator) error { return m.To(prev) })
	}
	return pool
}

// MigrateUp 把库迁移到最新版本。
func MigrateUp(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	withMigrator(t, pool, func(m *db.Migrator) error {
		_, _, err := m.Up(context.Background())
		return err
	})
}

func withMigrator(t *testing.T, pool *pgxpool.Pool, run func(*db.Migrator) error) {
	t.Helper()
	m, err := db.NewMigrator(pool.Config().ConnConfig.ConnString())
	if err != nil {
		t.Fatalf("创建迁移器失败: %v", err)
	}
	defer func() { _ = m.Close() }()
	if err := run(m); err != nil {
		t.Fatalf("迁移失败: %v", err)
	}
}

// previousVersion 返回内置迁移中小于 version 的最大版本号（0 = 没有更早的迁移）。
func previousVersion(t *testing.T, version uint) uint {
	t.Helper()
	names, err := fs.Glob(migrations.FS, "*.up.sql")
	if err != nil {
		t.Fatalf("列出迁移文件失败: %v", err)
	}
	var prev uint
	for _, name := range names {
		num, _, _ := strings.Cut(name, "_")
		v, err := strconv.ParseUint(num, 10, 64)
		if err != nil {
			t.Fatalf("迁移文件名不合法 %s: %v", name, err)
		}
		if uint(v) < version && uint(v) > prev {
			prev = uint(v)
		}
	}
	return prev
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
