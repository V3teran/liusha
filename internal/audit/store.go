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

// Append 单条插入审计事件。
// 必填：Actor / Action / TargetKind / TargetID；Metadata 为空时落 '{}'。
func (s *Store) Append(ctx context.Context, e Event) (int64, error) {
	if e.Actor == "" || e.Action == "" {
		return 0, fmt.Errorf("audit: Actor + Action 必填")
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
