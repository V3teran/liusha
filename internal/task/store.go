package task

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 task 表的所有持久化操作（合并原 activescan.Store + passivesession.Store）。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT / RETURNING 路径的统一列序，与 scan() 字段一一对应。
const colsSelect = "id, mode, brief, target_host, status, heartbeat_at, paused_ms, " +
	"created_at, ended_at, error_message"

const (
	defaultListLimit = 20
	maxListLimit     = 200
)

// Create 建一个新 task。
//   - active：Brief 必填，TargetHost 可空。
//   - passive：TargetHost 必填，Brief 留空。
func (s *Store) Create(ctx context.Context, p NewParams) (Task, error) {
	switch p.Mode {
	case ModeActive:
		if p.Brief == "" {
			return Task{}, fmt.Errorf("create active task: brief 必填")
		}
	case ModePassive:
		if p.TargetHost == "" {
			return Task{}, fmt.Errorf("create passive task: target_host 必填")
		}
	default:
		return Task{}, fmt.Errorf("create task: 非法 mode %q", p.Mode)
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO task (mode, brief, target_host, status)
		VALUES ($1, $2, $3, 'active')
		RETURNING `+colsSelect, string(p.Mode), p.Brief, p.TargetHost)
	var t Task
	if err := scan(row, &t); err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}
	return t, nil
}

// List 按 created_at DESC 列出最近的 task。mode 为空时不过滤（两模式混列）。
func (s *Store) List(ctx context.Context, mode Mode, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	q := "SELECT " + colsSelect + " FROM task"
	args := []any{}
	if mode != "" {
		q += " WHERE mode=$1"
		args = append(args, string(mode))
	}
	q += " ORDER BY created_at DESC LIMIT $" + fmt.Sprint(len(args)+1)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		var t Task
		if err := scan(rows, &t); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetByID 按主键读取（不限 status）。
func (s *Store) GetByID(ctx context.Context, id string) (Task, error) {
	row := s.pool.QueryRow(ctx, "SELECT "+colsSelect+" FROM task WHERE id=$1", id)
	var t Task
	if err := scan(row, &t); err != nil {
		return Task{}, fmt.Errorf("get task %s: %w", id, err)
	}
	return t, nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scan 是 colsSelect 列序的统一反序列化点。
func scan(r scanner, t *Task) error {
	var mode, status string
	if err := r.Scan(&t.ID, &mode, &t.Brief, &t.TargetHost, &status,
		&t.HeartbeatAt, &t.PausedMs,
		&t.CreatedAt, &t.EndedAt, &t.ErrorMessage); err != nil {
		return err
	}
	t.Mode = Mode(mode)
	t.Status = Status(status)
	return nil
}
