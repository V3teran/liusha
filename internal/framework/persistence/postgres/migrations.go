package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Migration 是数据库迁移。
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// Migrations 是所有迁移的列表。
var Migrations = []Migration{
	{
		Version: 1,
		Name:    "create_framework_tables",
		SQL: `
-- 任务状态表
CREATE TABLE IF NOT EXISTS framework_task_state (
    task_id TEXT PRIMARY KEY,
    state_json JSONB NOT NULL,
    version BIGINT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 检查点表
CREATE TABLE IF NOT EXISTS framework_checkpoint (
    id TEXT PRIMARY KEY,
    task_id TEXT NOT NULL,
    state_snapshot JSONB NOT NULL,
    phase TEXT NOT NULL,
    component_states JSONB NOT NULL,
    labels JSONB,
    size_bytes BIGINT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 索引
CREATE INDEX IF NOT EXISTS idx_checkpoint_task_time
    ON framework_checkpoint(task_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_task_state_updated
    ON framework_task_state(updated_at DESC);

-- 迁移版本表
CREATE TABLE IF NOT EXISTS framework_migrations (
    version INT PRIMARY KEY,
    name TEXT NOT NULL,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
`,
	},
}

// Migrate 执行数据库迁移。
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	// 获取当前版本
	currentVersion, err := getCurrentVersion(ctx, pool)
	if err != nil {
		return fmt.Errorf("get current version: %w", err)
	}

	// 执行未应用的迁移
	for _, migration := range Migrations {
		if migration.Version <= currentVersion {
			continue
		}

		// 开始事务
		tx, err := pool.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin transaction: %w", err)
		}

		// 执行迁移 SQL
		if _, err := tx.Exec(ctx, migration.SQL); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("execute migration %d: %w", migration.Version, err)
		}

		// 记录迁移版本
		recordSQL := `
			INSERT INTO framework_migrations (version, name, applied_at)
			VALUES ($1, $2, NOW())
		`
		if _, err := tx.Exec(ctx, recordSQL, migration.Version, migration.Name); err != nil {
			tx.Rollback(ctx)
			return fmt.Errorf("record migration %d: %w", migration.Version, err)
		}

		// 提交事务
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit migration %d: %w", migration.Version, err)
		}

		fmt.Printf("Applied migration %d: %s\n", migration.Version, migration.Name)
	}

	return nil
}

// getCurrentVersion 获取当前迁移版本。
func getCurrentVersion(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	// 检查迁移表是否存在
	checkSQL := `
		SELECT EXISTS (
			SELECT FROM information_schema.tables
			WHERE table_name = 'framework_migrations'
		)
	`

	var exists bool
	if err := pool.QueryRow(ctx, checkSQL).Scan(&exists); err != nil {
		return 0, err
	}

	if !exists {
		return 0, nil // 尚未迁移
	}

	// 获取最新版本
	versionSQL := `SELECT COALESCE(MAX(version), 0) FROM framework_migrations`

	var version int
	if err := pool.QueryRow(ctx, versionSQL).Scan(&version); err != nil {
		return 0, err
	}

	return version, nil
}

// Rollback 回滚迁移（暂不实现，需要每个迁移提供回滚 SQL）。
func Rollback(ctx context.Context, pool *pgxpool.Pool, targetVersion int) error {
	return fmt.Errorf("rollback not implemented yet")
}

// Version 获取当前数据库版本。
func Version(ctx context.Context, pool *pgxpool.Pool) (int, error) {
	return getCurrentVersion(ctx, pool)
}
