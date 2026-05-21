package agentrun

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

)

// Store 封装 agent_task 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 scanTask() 的字段顺序一一对应。
// parent_id 用 COALESCE 把 NULL 折成空串 → Go 层 ReactRun.CommanderID = ""（独立任务）。
const colsSelect = `id, ` +
	`owner_type, ` +
	`owner_id::text AS owner_id, ` +
	`COALESCE(commander_id::text, '') AS commander_id, ` +
	`role, input, result, status, created_at, updated_at`

// Create 插入一行 pending 任务，返回新 id。Input 为 nil 时落空对象。
// CommanderID 空串用 NULLIF 转 PG NULL，匹配 0037 partial index（WHERE parent_id IS NOT NULL）。
// 0041 之后 owner_type/owner_id NOT NULL，必填。
func (s *Store) Create(ctx context.Context, p NewParams) (string, error) {
	if p.Input == nil {
		p.Input = json.RawMessage("{}")
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO agent_task (owner_type, owner_id, commander_id, role, input)
		VALUES ($1, $2::uuid, NULLIF($3, '')::uuid, $4, $5)
		RETURNING id`,
		p.OwnerType, p.OwnerID, p.CommanderID, p.Role,
		[]byte(p.Input),
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

// SetAborted 把 pending|running 任务推进到 aborted（用于 owner abort 级联）。
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
func (s *Store) GetByID(ctx context.Context, id string) (ReactRun, error) {
	row := s.pool.QueryRow(ctx, `SELECT `+colsSelect+` FROM agent_task WHERE id=$1`, id)
	var t ReactRun
	if err := scanTask(row, &t); err != nil {
		return ReactRun{}, fmt.Errorf("get task %s: %w", id, err)
	}
	return t, nil
}

// ListByOwnerID 按 created_at 升序列出 owner_id 下的任务，最多 limit 条。
func (s *Store) ListByOwnerID(ctx context.Context, ownerID string, limit int) ([]ReactRun, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM agent_task
		WHERE owner_id=$1::uuid
		ORDER BY created_at ASC
		LIMIT $2`, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks: %w", err)
	}
	defer rows.Close()

	var out []ReactRun
	for rows.Next() {
		var t ReactRun
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

// ListByOwner 按 owner_type+owner_id 升序列出任务（polymorphic canonical 路径）。
func (s *Store) ListByOwner(ctx context.Context, ownerType, ownerID string, limit int) ([]ReactRun, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM agent_task
		WHERE owner_type=$1 AND owner_id=$2::uuid
		ORDER BY created_at ASC
		LIMIT $3`, ownerType, ownerID, limit)
	if err != nil {
		return nil, fmt.Errorf("list tasks by owner: %w", err)
	}
	defer rows.Close()

	var out []ReactRun
	for rows.Next() {
		var t ReactRun
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

// scanner 抽象 pgx.Row / pgx.Rows 的 Scan 方法。
type scanner interface {
	Scan(dest ...any) error
}

// scanTask 是 colsSelect 列序的统一反序列化点。
func scanTask(r scanner, t *ReactRun) error {
	var input, result []byte
	if err := r.Scan(
		&t.ID,
		&t.OwnerType, &t.OwnerID,
		&t.CommanderID, &t.Role,
		&input, &result, &t.Status, &t.CreatedAt, &t.UpdatedAt,
	); err != nil {
		return err
	}
	t.Input, t.Result = input, result
	return nil
}
