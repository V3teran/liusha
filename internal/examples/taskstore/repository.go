// Package taskstore 使用 sqlc 生成的代码访问 task 表
//
// 这是一个示例，展示如何在新功能中使用 sqlc。
// 优势：
//   - 编译时类型安全
//   - 防止 SQL 注入（自动使用预编译语句）
//   - 无需手写 SQL 解析代码
//   - 易于测试和维护
package taskstore

import (
	"context"
	"database/sql"
	"time"

	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/sqlc"
)

// Repository 封装 task 相关的数据库操作
type Repository struct {
	queries *sqlc.Queries
	db      *sql.DB
}

// NewRepository 创建新的 Repository
func NewRepository(db *sql.DB) *Repository {
	return &Repository{
		queries: sqlc.New(db),
		db:      db,
	}
}

// GetByID 根据 ID 获取任务
func (r *Repository) GetByID(ctx context.Context, id string) (sqlc.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, constants.DBQueryTimeout)
	defer cancel()

	return r.queries.GetTaskByID(ctx, id)
}

// List 列出任务（分页）
func (r *Repository) List(ctx context.Context, limit, offset int32) ([]sqlc.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, constants.DBQueryTimeout)
	defer cancel()

	return r.queries.ListTasks(ctx, limit, offset)
}

// ListByStatus 根据状态列出任务
func (r *Repository) ListByStatus(ctx context.Context, status string) ([]sqlc.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, constants.DBQueryTimeout)
	defer cancel()

	return r.queries.ListTasksByStatus(ctx, status)
}

// Create 创建新任务
func (r *Repository) Create(ctx context.Context, id, mode, targetHost, status string, targetPort sql.NullInt32, brief sql.NullString) (sqlc.Task, error) {
	ctx, cancel := context.WithTimeout(ctx, constants.DBQueryTimeout)
	defer cancel()

	now := time.Now()
	return r.queries.CreateTask(ctx, id, mode, targetHost, targetPort, brief, status, now, now)
}

// UpdateStatus 更新任务状态
func (r *Repository) UpdateStatus(ctx context.Context, id, status string) error {
	ctx, cancel := context.WithTimeout(ctx, constants.DBQueryTimeout)
	defer cancel()

	return r.queries.UpdateTaskStatus(ctx, id, status, time.Now())
}

// Delete 删除任务
func (r *Repository) Delete(ctx context.Context, id string) error {
	ctx, cancel := context.WithTimeout(ctx, constants.DBQueryTimeout)
	defer cancel()

	return r.queries.DeleteTask(ctx, id)
}

// CountByStatus 统计指定状态的任务数量
func (r *Repository) CountByStatus(ctx context.Context, status string) (int64, error) {
	ctx, cancel := context.WithTimeout(ctx, constants.DBQueryTimeout)
	defer cancel()

	return r.queries.CountTasksByStatus(ctx, status)
}

// WithTransaction 在事务中执行操作
func (r *Repository) WithTransaction(ctx context.Context, fn func(*sqlc.Queries) error) error {
	ctx, cancel := context.WithTimeout(ctx, constants.DBTransactionTimeout)
	defer cancel()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback() // 忽略 Rollback 错误，因为 Commit 成功时 Rollback 会失败
	}()

	qtx := r.queries.WithTx(tx)
	if err := fn(qtx); err != nil {
		return err
	}

	return tx.Commit()
}

// Example 使用示例
func Example(db *sql.DB) error {
	repo := NewRepository(db)
	ctx := context.Background()

	// 1. 创建任务
	created, err := repo.Create(
		ctx,
		"task-123",
		"passive",
		"example.com",
		"pending",
		sql.NullInt32{Int32: 443, Valid: true},
		sql.NullString{String: "测试任务", Valid: true},
	)
	if err != nil {
		return err
	}
	_ = created

	// 2. 查询任务
	fetched, err := repo.GetByID(ctx, "task-123")
	if err != nil {
		return err
	}
	_ = fetched

	// 3. 更新状态
	if err := repo.UpdateStatus(ctx, "task-123", "running"); err != nil {
		return err
	}

	// 4. 列出任务
	tasks, err := repo.List(ctx, 10, 0)
	if err != nil {
		return err
	}
	_ = tasks

	// 5. 统计任务
	count, err := repo.CountByStatus(ctx, "running")
	if err != nil {
		return err
	}
	_ = count

	// 6. 事务示例
	err = repo.WithTransaction(ctx, func(q *sqlc.Queries) error {
		// 在事务中执行多个操作
		if err := q.UpdateTaskStatus(ctx, "task-123", "completed", time.Now()); err != nil {
			return err
		}

		// 可以继续执行更多操作...
		return nil
	})

	return err
}
