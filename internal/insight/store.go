package insight

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 是洞察黑板的 PostgreSQL 持久化层。
type Store struct {
	pool *pgxpool.Pool
}

// NewStore 创建 Store。
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Append 追加一条洞察到指定 assignment 的黑板。
func (s *Store) Append(ctx context.Context, assignmentID string, insight Insight) error {
	if assignmentID == "" {
		return fmt.Errorf("insight.Store.Append: assignmentID 必填")
	}
	if insight.Summary == "" {
		return fmt.Errorf("insight.Store.Append: summary 必填")
	}
	if insight.Category == "" {
		insight.Category = CategoryNote
	}
	if insight.Priority == "" {
		insight.Priority = PriorityMedium
	}
	if insight.Confidence == "" {
		insight.Confidence = ConfidencePossible
	}
	if insight.Confidence == "" {
		insight.Confidence = ConfidencePossible
	}

	query := `
		INSERT INTO insight (
			assignment_id, category, priority, confidence,
			summary, body, tags,
			source_task_id, source_agent_id,
			created_at, updated_at
		)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
	`
	_, err := s.pool.Exec(ctx, query,
		assignmentID,
		insight.Category,
		insight.Priority,
		insight.Confidence,
		insight.Summary,
		insight.Body,
		insight.Tags,
		insight.SourceTaskID,
		insight.SourceAgentID,
		insight.CreatedAt,
		insight.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insight.Store.Append: %w", err)
	}

	return nil
}

// List 返回指定 assignment 的所有洞察，按创建时间倒序。
func (s *Store) List(ctx context.Context, assignmentID string, limit int) ([]Insight, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("insight.Store.List: assignmentID 必填")
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
		FROM insight
		WHERE assignment_id = $1
		ORDER BY created_at DESC
		LIMIT $2
	`

	rows, err := s.pool.Query(ctx, query, assignmentID, limit)
	if err != nil {
		return nil, fmt.Errorf("insight.Store.List: %w", err)
	}
	defer rows.Close()

	var insights []Insight
	for rows.Next() {
		var i Insight
		err := rows.Scan(
			&i.ID, &i.AssignmentID,
			&i.Category, &i.Priority, &i.Confidence,
			&i.Summary, &i.Body, &i.Tags,
			&i.SourceTaskID, &i.SourceAgentID,
			&i.CreatedAt, &i.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("insight.Store.List: scan: %w", err)
		}
		insights = append(insights, i)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insight.Store.List: rows: %w", err)
	}

	return insights, nil
}

// ListByPriority 返回指定 assignment 和优先级的洞察。
func (s *Store) ListByPriority(ctx context.Context, assignmentID string, priority Priority, limit int) ([]Insight, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("insight.Store.ListByPriority: assignmentID 必填")
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
		FROM insight
		WHERE assignment_id = $1 AND priority = $2
		ORDER BY created_at DESC
		LIMIT $3
	`

	rows, err := s.pool.Query(ctx, query, assignmentID, priority, limit)
	if err != nil {
		return nil, fmt.Errorf("insight.Store.ListByPriority: %w", err)
	}
	defer rows.Close()

	var insights []Insight
	for rows.Next() {
		var i Insight
		err := rows.Scan(
			&i.ID, &i.AssignmentID,
			&i.Category, &i.Priority, &i.Confidence,
			&i.Summary, &i.Body, &i.Tags,
			&i.SourceTaskID, &i.SourceAgentID,
			&i.CreatedAt, &i.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("insight.Store.ListByPriority: scan: %w", err)
		}
		insights = append(insights, i)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insight.Store.ListByPriority: rows: %w", err)
	}

	return insights, nil
}

// ListByCategory 返回指定 assignment 和分类的洞察。
func (s *Store) ListByCategory(ctx context.Context, assignmentID string, category Category, limit int) ([]Insight, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("insight.Store.ListByCategory: assignmentID 必填")
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
		FROM insight
		WHERE assignment_id = $1 AND category = $2
		ORDER BY created_at DESC
		LIMIT $3
	`

	rows, err := s.pool.Query(ctx, query, assignmentID, category, limit)
	if err != nil {
		return nil, fmt.Errorf("insight.Store.ListByCategory: %w", err)
	}
	defer rows.Close()

	var insights []Insight
	for rows.Next() {
		var i Insight
		err := rows.Scan(
			&i.ID, &i.AssignmentID,
			&i.Category, &i.Priority, &i.Confidence,
			&i.Summary, &i.Body, &i.Tags,
			&i.SourceTaskID, &i.SourceAgentID,
			&i.CreatedAt, &i.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("insight.Store.ListByCategory: scan: %w", err)
		}
		insights = append(insights, i)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insight.Store.ListByCategory: rows: %w", err)
	}

	return insights, nil
}

// ReadRecent 返回指定 assignment 的近期洞察，按优先级分组。
// 用于构建 prompt 时展示洞察黑板。
func (s *Store) ReadRecent(ctx context.Context, assignmentID string) (map[Priority]map[Category][]Insight, error) {
	insights, err := s.List(ctx, assignmentID, 100)
	if err != nil {
		return nil, err
	}

	// 按 priority → category 两级分组
	grouped := make(map[Priority]map[Category][]Insight)
	for _, i := range insights {
		if grouped[i.Priority] == nil {
			grouped[i.Priority] = make(map[Category][]Insight)
		}
		grouped[i.Priority][i.Category] = append(grouped[i.Priority][i.Category], i)
	}

	return grouped, nil
}
