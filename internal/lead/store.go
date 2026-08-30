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
	if entry.Summary == "" {
		return fmt.Errorf("lead.Store.Append: summary 必填")
	}
	if entry.Category == "" {
		entry.Category = CategoryNote
	}
	if entry.Priority == "" {
		entry.Priority = PriorityMedium
	}
	if entry.Confidence == "" {
		entry.Confidence = ConfidencePossible
	}
	if entry.Confidence == "" {
		entry.Confidence = ConfidencePossible
	}

	query := `
		INSERT INTO lead (
			assignment_id, category, priority, confidence,
			summary, body, tags,
			source_task_id, source_agent_id,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := s.pool.Exec(ctx, query,
		assignmentID,
		entry.Category,
		entry.Priority,
		entry.Confidence,
		entry.Summary,
		entry.Body,
		entry.Tags,
		entry.SourceTaskID,
		entry.SourceAgentID,
		entry.CreatedAt,
		entry.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("lead.Store.Append: %w", err)
	}

	return nil
}

// List 返回指定 assignment 的所有情报，按创建时间倒序。
func (s *Store) List(ctx context.Context, assignmentID string, limit int) ([]Entry, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("lead.Store.List: assignmentID 必填")
	}
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT
			id, assignment_id,
			category, priority, confidence,
			summary, body, tags,
			source_task_id, source_agent_id,
			created_at, updated_at
		FROM lead
		WHERE assignment_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := s.pool.Query(ctx, query, assignmentID, limit)
	if err != nil {
		return nil, fmt.Errorf("lead.Store.List: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		err := rows.Scan(
			&e.ID, &e.AssignmentID,
			&e.Category, &e.Priority, &e.Confidence,
			&e.Summary, &e.Body, &e.Tags,
			&e.SourceTaskID, &e.SourceAgentID,
			&e.CreatedAt, &e.UpdatedAt,
		)
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

// ListByPriority 返回指定 assignment 和优先级的情报。
func (s *Store) ListByPriority(ctx context.Context, assignmentID string, priority Priority, limit int) ([]Entry, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("lead.Store.ListByPriority: assignmentID 必填")
	}
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT
			id, assignment_id,
			category, priority, confidence,
			summary, body, tags,
			source_task_id, source_agent_id,
			created_at, updated_at
		FROM lead
		WHERE assignment_id = $1 AND priority = $2
		ORDER BY created_at DESC
		LIMIT $3
	`

	rows, err := s.pool.Query(ctx, query, assignmentID, priority, limit)
	if err != nil {
		return nil, fmt.Errorf("lead.Store.ListByPriority: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		err := rows.Scan(
			&e.ID, &e.AssignmentID,
			&e.Category, &e.Priority, &e.Confidence,
			&e.Summary, &e.Body, &e.Tags,
			&e.SourceTaskID, &e.SourceAgentID,
			&e.CreatedAt, &e.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("lead.Store.ListByPriority: scan: %w", err)
		}
		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lead.Store.ListByPriority: rows: %w", err)
	}

	return entries, nil
}

// ListByCategory 返回指定 assignment 和分类的情报。
func (s *Store) ListByCategory(ctx context.Context, assignmentID string, category Category, limit int) ([]Entry, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("lead.Store.ListByCategory: assignmentID 必填")
	}
	if limit <= 0 {
		limit = 100
	}

	query := `
		SELECT
			id, assignment_id,
			category, priority, confidence,
			summary, body, tags,
			source_task_id, source_agent_id,
			created_at, updated_at
		FROM lead
		WHERE assignment_id = $1 AND category = $2
		ORDER BY created_at DESC
		LIMIT $3
	`

	rows, err := s.pool.Query(ctx, query, assignmentID, category, limit)
	if err != nil {
		return nil, fmt.Errorf("lead.Store.ListByCategory: %w", err)
	}
	defer rows.Close()

	var entries []Entry
	for rows.Next() {
		var e Entry
		err := rows.Scan(
			&e.ID, &e.AssignmentID,
			&e.Category, &e.Priority, &e.Confidence,
			&e.Summary, &e.Body, &e.Tags,
			&e.SourceTaskID, &e.SourceAgentID,
			&e.CreatedAt, &e.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("lead.Store.ListByCategory: scan: %w", err)
		}
		entries = append(entries, e)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("lead.Store.ListByCategory: rows: %w", err)
	}

	return entries, nil
}

// ReadRecent 返回指定 assignment 的近期情报，按优先级分组。
// 用于构建 prompt 时展示情报黑板。
func (s *Store) ReadRecent(ctx context.Context, assignmentID string) (map[Priority]map[Category][]Entry, error) {
	entries, err := s.List(ctx, assignmentID, 100)
	if err != nil {
		return nil, err
	}

	// 按 priority → category 两级分组
	grouped := make(map[Priority]map[Category][]Entry)
	for _, e := range entries {
		if grouped[e.Priority] == nil {
			grouped[e.Priority] = make(map[Category][]Entry)
		}
		grouped[e.Priority][e.Category] = append(grouped[e.Priority][e.Category], e)
	}

	return grouped, nil
}
