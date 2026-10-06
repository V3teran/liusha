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
	if insight.Tags == nil {
		insight.Tags = []string{}
	}

	query := `
		INSERT INTO insight (
			assignment_id, category, priority, confidence,
			summary, body, tags,
			source_task_id, source_agent_run_id,
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
		insight.SourceAgentRunID,
		insight.CreatedAt,
		insight.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("insight.Store.Append: %w", err)
	}

	return nil
}

// list 是 List/ListByPriority/ListByCategory 的公共实现（where 已含占位符，args 与之对应）。
func (s *Store) list(ctx context.Context, label, where string, args []any, orderBy string, limit int) ([]Insight, error) {
	if limit <= 0 {
		limit = 100
	}
	if orderBy == "" {
		orderBy = "created_at DESC"
	}
	query := fmt.Sprintf(`
		SELECT
			id, assignment_id,
			category, priority, confidence,
			summary, body, tags,
			source_task_id, source_agent_run_id,
			created_at, updated_at
		FROM insight
		WHERE %s
		ORDER BY %s
		LIMIT $%d
	`, where, orderBy, len(args)+1)
	args = append(args, limit)

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("insight.Store.%s: %w", label, err)
	}
	defer rows.Close()

	var insights []Insight
	for rows.Next() {
		var i Insight
		err := rows.Scan(
			&i.ID, &i.AssignmentID,
			&i.Category, &i.Priority, &i.Confidence,
			&i.Summary, &i.Body, &i.Tags,
			&i.SourceTaskID, &i.SourceAgentRunID,
			&i.CreatedAt, &i.UpdatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("insight.Store.%s: scan: %w", label, err)
		}
		insights = append(insights, i)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("insight.Store.%s: rows: %w", label, err)
	}

	return insights, nil
}

// List 返回指定 assignment 的全部洞察（最新在前）。
func (s *Store) List(ctx context.Context, assignmentID string, limit int) ([]Insight, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("insight.Store.List: assignmentID 必填")
	}
	return s.list(ctx, "List", "assignment_id = $1", []any{assignmentID}, "", limit)
}

// ListByPriority 返回指定 assignment 和优先级的洞察。
func (s *Store) ListByPriority(ctx context.Context, assignmentID string, priority Priority, limit int) ([]Insight, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("insight.Store.ListByPriority: assignmentID 必填")
	}
	return s.list(ctx, "ListByPriority", "assignment_id = $1 AND priority = $2", []any{assignmentID, priority}, "created_at DESC", limit)
}

// ListByCategory 返回指定 assignment 和分类的洞察。
func (s *Store) ListByCategory(ctx context.Context, assignmentID string, category Category, limit int) ([]Insight, error) {
	if assignmentID == "" {
		return nil, fmt.Errorf("insight.Store.ListByCategory: assignmentID 必填")
	}
	return s.list(ctx, "ListByCategory", "assignment_id = $1 AND category = $2", []any{assignmentID, category}, "created_at DESC", limit)
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
