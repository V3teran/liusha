package window

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 封装 traffic_window 表的所有持久化操作。
type Store struct{ pool *pgxpool.Pool }

// NewStore 用 pgxpool 构造 Store。
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// OpenOrAppend 把 ref 原子追加到当前 engagement 的 open window；
// 若没有 open window 则懒创建。返回 (windowID, justClosed, err)：
// 当 append 后 flows 数量 ≥ batchLimit 时把窗口置为 closed，justClosed=true。
//
// 通过事务 + SELECT ... FOR UPDATE 保证：同一 engagement 同时只有 1 个 open window，
// 并发追加按行锁串行化。
func (s *Store) OpenOrAppend(ctx context.Context, engagementID string, ref FlowRef, batchLimit int) (string, bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var (
		id    string
		flows []byte
	)
	err = tx.QueryRow(ctx, `
		SELECT id, flows FROM traffic_window
		WHERE engagement_id=$1 AND status='open'
		ORDER BY started_at ASC LIMIT 1
		FOR UPDATE`, engagementID).Scan(&id, &flows)
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		if err := tx.QueryRow(ctx, `
			INSERT INTO traffic_window (engagement_id, flows, status)
			VALUES ($1, '[]'::jsonb, 'open')
			RETURNING id`, engagementID).Scan(&id); err != nil {
			return "", false, fmt.Errorf("insert open window: %w", err)
		}
		flows = []byte("[]")
	case err != nil:
		return "", false, fmt.Errorf("lock open window: %w", err)
	}

	var arr []FlowRef
	if err := json.Unmarshal(flows, &arr); err != nil {
		return "", false, fmt.Errorf("unmarshal flows: %w", err)
	}
	arr = append(arr, ref)
	enc, err := json.Marshal(arr)
	if err != nil {
		return "", false, fmt.Errorf("marshal flows: %w", err)
	}

	justClosed := len(arr) >= batchLimit
	q := `UPDATE traffic_window SET flows=$1 WHERE id=$2`
	if justClosed {
		q = `UPDATE traffic_window SET flows=$1, status='closed', closed_at=now() WHERE id=$2`
	}
	if _, err := tx.Exec(ctx, q, enc, id); err != nil {
		return "", false, fmt.Errorf("update window: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("commit: %w", err)
	}
	return id, justClosed, nil
}

// CloseExpired 把超过 maxAgeSec 秒未关闭的 open window 批量置为 closed，
// 用于兜底（兜住低流量场景下窗口长期不满 batchLimit 的问题）。
func (s *Store) CloseExpired(ctx context.Context, engagementID string, maxAgeSec int) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE traffic_window
		SET status='closed', closed_at=now()
		WHERE engagement_id=$1 AND status='open'
		  AND now() - started_at > make_interval(secs => $2)`, engagementID, maxAgeSec)
	if err != nil {
		return fmt.Errorf("close expired windows: %w", err)
	}
	return nil
}

// ListClosed 列出指定 engagement 的 closed（未 consumed）窗口，按 started_at 升序。
func (s *Store) ListClosed(ctx context.Context, engagementID string, limit int) ([]Window, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, engagement_id, flows, status, started_at, closed_at
		FROM traffic_window
		WHERE engagement_id=$1 AND status='closed'
		ORDER BY started_at ASC LIMIT $2`, engagementID, limit)
	if err != nil {
		return nil, fmt.Errorf("query closed windows: %w", err)
	}
	defer rows.Close()

	var out []Window
	for rows.Next() {
		var (
			w     Window
			flows []byte
		)
		if err := rows.Scan(&w.ID, &w.EngagementID, &flows, &w.Status, &w.StartedAt, &w.ClosedAt); err != nil {
			return nil, fmt.Errorf("scan window: %w", err)
		}
		if err := json.Unmarshal(flows, &w.Flows); err != nil {
			return nil, fmt.Errorf("unmarshal flows: %w", err)
		}
		out = append(out, w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate windows: %w", err)
	}
	return out, nil
}

// MarkConsumed 把 closed 窗口推进到 consumed。仅允许从 closed 转换；
// 若当前不是 closed（still open 或已 consumed），返回错误。
func (s *Store) MarkConsumed(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx,
		`UPDATE traffic_window SET status='consumed' WHERE id=$1 AND status='closed'`, id)
	if err != nil {
		return fmt.Errorf("mark consumed %s: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("window %s not in closed state", id)
	}
	return nil
}
