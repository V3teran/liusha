package task

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 agent_task 表的所有持久化操作。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 scanTask() 的字段顺序一一对应。
// v1.1：删除 parent_task_id 列（子 ReAct 同进程嵌套，不再入 PG，无父子关系）。
const colsSelect = `id, engagement_id, role, skill, input, budget, result, status, created_at, updated_at`

// Create 插入一行 pending 任务，返回新 id。Input/Budget 为 nil 时落空对象。
func (s *Store) Create(ctx context.Context, p NewParams) (string, error) {
	if p.Input == nil {
		p.Input = json.RawMessage("{}")
	}
	if p.Budget == nil {
		p.Budget = json.RawMessage("{}")
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_task (engagement_id, role, skill, input, budget)
		VALUES ($1,$2,$3,$4,$5)
		RETURNING id`,
		p.EngagementID, p.Role, p.Skill,
		[]byte(p.Input), []byte(p.Budget),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert task: %w", err)
	}
	return id, nil
}

// SetRunning 把 pending 任务推进到 running；非 pending 视为非法转换。
func (s *Store) SetRunning(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_task SET status='running', updated_at=now()
		WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return fmt.Errorf("set running %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task %s not in pending state", id)
	}
	return nil
}

// SetDone 把 pending|running 任务推进到 done，写入 result。
func (s *Store) SetDone(ctx context.Context, id string, result json.RawMessage) error {
	if result == nil {
		result = json.RawMessage("{}")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_task SET status='done', result=$1, updated_at=now()
		WHERE id=$2 AND status IN ('pending','running')`, []byte(result), id)
	if err != nil {
		return fmt.Errorf("set done %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task %s not in pending|running state", id)
	}
	return nil
}

// SetError 把 pending|running 任务推进到 error；errMsg 序列化为 {"error": ...}。
func (s *Store) SetError(ctx context.Context, id string, errMsg string) error {
	body, _ := json.Marshal(map[string]string{"error": errMsg})
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_task SET status='error', result=$1, updated_at=now()
		WHERE id=$2 AND status IN ('pending','running')`, body, id)
	if err != nil {
		return fmt.Errorf("set error %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task %s not in pending|running state", id)
	}
	return nil
}

// SetAborted 把 pending|running 任务推进到 aborted（用于 engagement abort 级联）。
func (s *Store) SetAborted(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_task SET status='aborted', updated_at=now()
		WHERE id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		return fmt.Errorf("set aborted %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("task %s not in pending|running state", id)
	}
	return nil
}

// GetByID 按主键读取任务行。
func (s *Store) GetByID(ctx context.Context, id string) (Task, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+colsSelect+` FROM agent_task WHERE id=$1`, id)
	var t Task
	if err := scanTask(row, &t); err != nil {
		return Task{}, fmt.Errorf("get task %s: %w", id, err)
	}
	return t, nil
}

// ListByEngagement 按 created_at 升序列出 engagement 的任务，最多 limit 条。
func (s *Store) ListByEngagement(ctx context.Context, engagementID string, limit int) ([]Task, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM agent_task
		WHERE engagement_id=$1
		ORDER BY created_at ASC
		LIMIT $2`, engagementID, limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		var t Task
		if err := scanTask(rows, &t); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tasks: %w", err)
	}
	return out, nil
}

// CountInflightInEngagement 统计 engagement 下处于 pending|running 的任务总数（全局并发上限）。
func (s *Store) CountInflightInEngagement(ctx context.Context, engagementID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM agent_task
		WHERE engagement_id=$1 AND status IN ('pending','running')`, engagementID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count inflight in engagement: %w", err)
	}
	return n, nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scanTask 是 colsSelect 列序的统一反序列化点。
func scanTask(r scanner, t *Task) error {
	var input, budget, result []byte
	if err := r.Scan(
		&t.ID, &t.EngagementID, &t.Role, &t.Skill,
		&input, &budget, &result, &t.Status, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return err
	}
	t.Input, t.Budget, t.Result = input, budget, result
	return nil
}
