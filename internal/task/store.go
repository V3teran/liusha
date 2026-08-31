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
const colsSelect = "id, assignment_id, brief, target_host, status, heartbeat_at, paused_ms, " +
	"created_at, ended_at, error_message"

const (
	defaultListLimit = 20
	maxListLimit     = 200
)

// Create 建一个新 task。Brief 必填；TargetHost 可空（runner 回填）。
func (s *Store) Create(ctx context.Context, p NewParams) (Task, error) {
	if p.AssignmentID == "" {
		return Task{}, fmt.Errorf("create task: assignment_id 必填")
	}
	if p.Brief == "" {
		return Task{}, fmt.Errorf("create task: brief 必填")
	}
	row := s.pool.QueryRow(ctx, `
		INSERT INTO task (assignment_id, brief, target_host, status)
		VALUES ($1, $2, $3, 'active')
		RETURNING `+colsSelect, p.AssignmentID, p.Brief, p.TargetHost)
	var t Task
	if err := scan(row, &t); err != nil {
		return Task{}, fmt.Errorf("create task: %w", err)
	}
	return t, nil
}

// List 按 created_at DESC 列出最近的 task。
func (s *Store) List(ctx context.Context, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = defaultListLimit
	}
	if limit > maxListLimit {
		limit = maxListLimit
	}
	q := "SELECT " + colsSelect + " FROM task ORDER BY created_at DESC LIMIT $1"

	rows, err := s.pool.Query(ctx, q, limit)
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
	var status string
	if err := r.Scan(&t.ID, &t.AssignmentID, &t.Brief, &t.TargetHost, &status,
		&t.HeartbeatAt, &t.PausedMs,
		&t.CreatedAt, &t.EndedAt, &t.ErrorMessage); err != nil {
		return err
	}
	t.Status = Status(status)
	return nil
}
