package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CreateEdge 创建边（幂等）。
func (s *PostgresGraphStore) CreateEdge(ctx context.Context, edge *GraphEdge) error {
	if edge == nil {
		return errors.New("edge cannot be nil")
	}
	if edge.From == "" || edge.To == "" {
		return errors.New("edge from/to cannot be empty")
	}
	if edge.Relation == "" {
		return errors.New("edge relation cannot be empty")
	}

	// 从 From 节点获取 task_id
	taskID, err := s.getNodeTaskID(ctx, edge.From)
	if err != nil {
		return fmt.Errorf("get task_id from node %s: %w", edge.From, err)
	}

	attrsJSON, err := json.Marshal(edge.Metadata)
	if err != nil {
		return fmt.Errorf("marshal edge metadata: %w", err)
	}

	query := `
		INSERT INTO ` + s.edgeTable + ` (task_id, src_id, rel, dst_id, attrs, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (task_id, src_id, rel, dst_id) DO NOTHING
	`

	now := time.Now()
	if !edge.CreatedAt.IsZero() {
		now = edge.CreatedAt
	}

	_, err = s.pool.Exec(ctx, query,
		taskID,
		edge.From,
		edge.Relation,
		edge.To,
		attrsJSON,
		now,
	)

	if err != nil {
		return fmt.Errorf("insert edge: %w", err)
	}

	return nil
}

// ListEdges 查询边列表。
func (s *PostgresGraphStore) ListEdges(ctx context.Context, query GraphEdgeQuery) ([]*GraphEdge, error) {
	whereParts := []string{}
	args := []interface{}{}
	argIndex := 1

	if query.From != "" {
		whereParts = append(whereParts, fmt.Sprintf("src_id = $%d", argIndex))
		args = append(args, query.From)
		argIndex++
	}

	if query.To != "" {
		whereParts = append(whereParts, fmt.Sprintf("dst_id = $%d", argIndex))
		args = append(args, query.To)
		argIndex++
	}

	if query.Relation != "" {
		whereParts = append(whereParts, fmt.Sprintf("rel = $%d", argIndex))
		args = append(args, query.Relation)
		argIndex++
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	limitClause := ""
	if query.Limit > 0 {
		limitClause = fmt.Sprintf("LIMIT $%d", argIndex)
		args = append(args, query.Limit)
	}

	sqlQuery := fmt.Sprintf(`
		SELECT src_id, dst_id, rel, attrs, created_at
		FROM `+s.edgeTable+`
		%s
		ORDER BY created_at DESC
		%s
	`, whereClause, limitClause)

	rows, err := s.pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query edges: %w", err)
	}
	defer rows.Close()

	var edges []*GraphEdge
	for rows.Next() {
		var edge GraphEdge
		var attrsJSON []byte

		err := rows.Scan(
			&edge.From,
			&edge.To,
			&edge.Relation,
			&attrsJSON,
			&edge.CreatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan edge: %w", err)
		}

		// 解析 metadata
		if len(attrsJSON) > 0 {
			if err := json.Unmarshal(attrsJSON, &edge.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal edge metadata: %w", err)
			}
		}

		edges = append(edges, &edge)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate edges: %w", err)
	}

	return edges, nil
}

// DeleteEdge 删除边。
func (s *PostgresGraphStore) DeleteEdge(ctx context.Context, from, to, relation string) error {
	if from == "" || to == "" || relation == "" {
		return errors.New("from/to/relation cannot be empty")
	}

	query := `
		DELETE FROM ` + s.edgeTable + `
		WHERE src_id = $1 AND dst_id = $2 AND rel = $3
	`

	result, err := s.pool.Exec(ctx, query, from, to, relation)
	if err != nil {
		return fmt.Errorf("delete edge: %w", err)
	}

	if result.RowsAffected() == 0 {
		return ErrGraphEdgeNotFound
	}

	return nil
}
