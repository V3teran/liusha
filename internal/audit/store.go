package audit

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 audit_log 表的所有持久化操作。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// colsSelect 是所有 SELECT 路径的统一列序，与 collect() 字段一一对应。
const colsSelect = "id, actor, action, target_kind, target_id, metadata, created_at"

// Append 单条插入审计事件。
// 必填：Actor / Action / TargetKind / TargetID；Metadata 为空时落 '{}'。
func (s *Store) Append(ctx context.Context, e Event) (int64, error) {
	if e.Actor == "" || e.Action == "" {
		return 0, fmt.Errorf("audit: Executor + Action 必填")
	}
	if e.TargetKind == "" || e.TargetID == "" {
		return 0, fmt.Errorf("audit: TargetKind + TargetID 必填")
	}
	meta := e.Metadata
	if len(meta) == 0 {
		meta = json.RawMessage("{}")
	}
	var id int64
	err := s.pool.QueryRow(ctx, `
		INSERT INTO audit_log (actor, action, target_kind, target_id, metadata)
		VALUES ($1, $2, $3, $4, $5)
		RETURNING id`,
		e.Actor, e.Action, e.TargetKind, e.TargetID, meta,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("append audit: %w", err)
	}
	return id, nil
}

// ListByActor 按 created_at DESC 列出指定 actor 最近的事件。
func (s *Store) ListByActor(ctx context.Context, actor string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM audit_log
		WHERE actor=$1
		ORDER BY created_at DESC, id DESC
		LIMIT $2`, actor, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit by actor: %w", err)
	}
	defer rows.Close()
	return collect(rows)
}

// ListByTarget 按 created_at DESC 列出指定 (target_kind, target_id) 的全部事件。
// 取证场景："这个 owner 何时被中止、被谁中止"。
func (s *Store) ListByTarget(ctx context.Context, kind, id string, limit int) ([]Event, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+colsSelect+`
		FROM audit_log
		WHERE target_kind=$1 AND target_id=$2
		ORDER BY created_at DESC, id DESC
		LIMIT $3`, kind, id, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit by target: %w", err)
	}
	defer rows.Close()
	return collect(rows)
}

// collect 通用列表收集器。
func collect(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]Event, error) {
	var out []Event
	for rows.Next() {
		var e Event
		if err := rows.Scan(
			&e.ID, &e.Actor, &e.Action, &e.TargetKind, &e.TargetID, &e.Metadata, &e.CreatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
