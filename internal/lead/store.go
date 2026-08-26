package lead

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 是情报黑板的 PostgreSQL 持久化层。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 创建 Store。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Append 追加一条情报到指定 assignment 的黑板。
func (s *Store) Append(ctx context.Context, assignmentID string, entry Entry) error {
	if assignmentID == "" {
		return fmt.Errorf("lead.Store.Append: assignmentID 必填")
	}
	if entry.Detail == "" {
		return fmt.Errorf("lead.Store.Append: detail 必填")
	}

	query := `
		INSERT INTO lead (assignment_id, kind, detail, executor_id, source_task_id, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
	`
	_, err := s.pool.Exec(ctx, query,
		assignmentID,
		entry.Kind,
		entry.Detail,
		entry.ExecutorID,
		entry.SourceTaskID,
		entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("lead.Store.Append: %w", err)
	}

	return nil
}

// List 返回指定 assignment 的所有情报（按时间倒序，最新的在前）。
// limit <= 0 时返回全部。
func (s *Store) List(ctx context.Context, assignmentID string, limit int) ([]Entry, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("lead.Store.List: assignmentID 必填")
	}

	query := `
		SELECT kind, detail, executor_id, source_task_id, created_at
		FROM lead
		WHERE assignment_id = $1
		ORDER BY created_at DESC
	`

	var args []interface{}
	args = append(args, assignmentID)

	if limit > 0 {
		query += " LIMIT $2"
		args = append(args, limit)
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lead.Store.List: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		err := rows.Scan(&e.Kind, &e.Detail, &e.ExecutorID, &e.SourceTaskID, &e.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("lead.Store.List: scan: %w", err)
		}
		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lead.Store.List: rows: %w", err)
	}

	return entries, nil
}

// ReadRecent 返回指定 assignment 的近期情报，按 Kind 分组。
// 用于构建 prompt 时展示情报黑板。
func (s *Store) ReadRecent(ctx context.Context, assignmentID string) (map[Kind][]Entry, error) {
	entries, err := s.List(ctx, assignmentID, 100) // 最多返回 100 条
	if err != nil {
		return nil, err
	}

	grouped := make(map[Kind][]Entry)
	for _, e := range entries {
		grouped[e.Kind] = append(grouped[e.Kind], e)
	}

	return grouped, nil
}
