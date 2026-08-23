package agentrun

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 agent_run 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 scanRun() 的字段顺序一一对应。
// planner_id 用 COALESCE 把 NULL 折成空串 → Go 层 Run.plannerID = ""（独立任务）。
const colsSelect = `id, ` +
	`task_id::text AS task_id, ` +
	`COALESCE(planner_id::text, '') AS planner_id, ` +
	`role, input, result, status, created_at, updated_at`

// Create 插入一行 pending agent 运行，返回新 id。Input 为 nil 时落空对象。
// plannerID 空串用 NULLIF 转 PG NULL。TaskID 必填（NOT NULL 外键）。
func (s *Store) Create(ctx context.Context, p NewParams) (string, error) {
	if p.Input == nil {
		p.Input = json.RawMessage("{}")
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_run (task_id, planner_id, role, input)
		VALUES ($1::uuid, NULLIF($2, '')::uuid, $3, $4)
		RETURNING id`,
		p.TaskID, p.plannerID, p.Role,
		[]byte(p.Input),
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("insert executor run: %w", err)
	}
	return id, nil
}

// SetRunning 把 pending agent 运行推进到 running；非 pending 视为非法转换。
func (s *Store) SetRunning(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET status='running', updated_at=now()
		WHERE id=$1 AND status='pending'`, id)
	if err != nil {
		return fmt.Errorf("set running %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("executor run %s not in pending state", id)
	}
	return nil
}

// SetDone 把 pending|running 推进到 done，写入 result。
func (s *Store) SetDone(ctx context.Context, id string, result json.RawMessage) error {
	if result == nil {
		result = json.RawMessage("{}")
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET status='done', result=$1, updated_at=now()
		WHERE id=$2 AND status IN ('pending','running')`, []byte(result), id)
	if err != nil {
		return fmt.Errorf("set done %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("executor run %s not in pending|running state", id)
	}
	return nil
}

// SetError 把 pending|running 推进到 error；errMsg 序列化为 {"error": ...}。
func (s *Store) SetError(ctx context.Context, id string, errMsg string) error {
	body, _ := json.Marshal(map[string]string{"error": errMsg})
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET status='error', result=$1, updated_at=now()
		WHERE id=$2 AND status IN ('pending','running')`, body, id)
	if err != nil {
		return fmt.Errorf("set error %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("executor run %s not in pending|running state", id)
	}
	return nil
}

// SetAborted 把 pending|running 推进到 aborted（用于 owner abort 级联）。
func (s *Store) SetAborted(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE agent_run SET status='aborted', updated_at=now()
		WHERE id=$1 AND status IN ('pending','running')`, id)
	if err != nil {
		return fmt.Errorf("set aborted %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("executor run %s not in pending|running state", id)
	}
	return nil
}

// GetByID 按主键读取 agent 运行行。
func (s *Store) GetByID(ctx context.Context, id string) (Run, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+colsSelect+` FROM agent_run WHERE id=$1`, id)
	var r Run
	if err := scanRun(row, &r); err != nil {
		return Run{}, fmt.Errorf("get executor run %s: %w", id, err)
	}
	return r, nil
}

// ListByTask 按 created_at 升序列出 task 下的 agent 运行，最多 limit 条。
func (s *Store) ListByTask(ctx context.Context, taskID string, limit int) ([]Run, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT `+colsSelect+` FROM agent_run WHERE task_id=$1::uuid ORDER BY created_at ASC LIMIT $2`,
		taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list executor runs by task: %w", err)
	}
	defer rows.Close()

	var out []Run
	for rows.Next() {
		var r Run
		if err := scanRun(rows, &r); err != nil {
			return nil, fmt.Errorf("scan executor run: %w", err)
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate executor runs: %w", err)
	}
	return out, nil
}

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scanRun 是 colsSelect 列序的统一反序列化点。
func scanRun(r scanner, t *Run) error {
	var input, result []byte
	if err := r.Scan(
		&t.ID,
		&t.TaskID,
		&t.plannerID, &t.Role,
		&input, &result, &t.Status, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return err
	}
	t.Input, t.Result = input, result
	return nil
}
