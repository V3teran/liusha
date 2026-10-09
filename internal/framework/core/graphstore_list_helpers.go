package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// buildWhereClause 构建 WHERE 子句
func buildWhereClause(query GraphNodeQuery) (string, []interface{}, int) {
	whereParts := []string{}
	args := []interface{}{}
	argIndex := 1

	if query.Kind != "" {
		whereParts = append(whereParts, fmt.Sprintf("kind = $%d", argIndex))
		args = append(args, query.Kind)
		argIndex++
	}

	if query.State != "" {
		whereParts = append(whereParts, fmt.Sprintf("state = $%d", argIndex))
		args = append(args, query.State)
		argIndex++
	}

	if query.MinConfidence > 0 {
		// confidence 存于 metadata["_confidence"]（浮点）
		whereParts = append(whereParts, fmt.Sprintf(
			"(metadata->>'_confidence')::float >= $%d", argIndex))
		args = append(args, query.MinConfidence)
		argIndex++
	}

	// Filters：task_id 走列（有索引），其余支持 metadata.xxx 路径
	for key, value := range query.Filters {
		if key == "task_id" {
			whereParts = append(whereParts, fmt.Sprintf("task_id = $%d", argIndex))
			args = append(args, value)
			argIndex++
			continue
		}
		if strings.HasPrefix(key, "metadata.") {
			field := strings.TrimPrefix(key, "metadata.")
			whereParts = append(whereParts, fmt.Sprintf(
				"metadata->>'%s' = $%d", field, argIndex))
			args = append(args, fmt.Sprint(value))
			argIndex++
		}
	}

	whereClause := ""
	if len(whereParts) > 0 {
		whereClause = "WHERE " + strings.Join(whereParts, " AND ")
	}

	return whereClause, args, argIndex
}

// buildOrderByClause 构建 ORDER BY 子句
func buildOrderByClause(query GraphNodeQuery) string {
	orderBy := "created_at DESC"
	if query.OrderBy != "" {
		if strings.HasPrefix(query.OrderBy, "-") {
			orderBy = strings.TrimPrefix(query.OrderBy, "-") + " DESC"
		} else {
			orderBy = query.OrderBy + " ASC"
		}
	}
	return orderBy
}

// buildPaginationClauses 构建分页子句
func buildPaginationClauses(query GraphNodeQuery, argIndex int) (string, string, []interface{}) {
	args := []interface{}{}

	limitClause := ""
	if query.Limit > 0 {
		limitClause = fmt.Sprintf("LIMIT $%d", argIndex)
		args = append(args, query.Limit)
		argIndex++
	}

	offsetClause := ""
	if query.Offset > 0 {
		offsetClause = fmt.Sprintf("OFFSET $%d", argIndex)
		args = append(args, query.Offset)
	}

	return limitClause, offsetClause, args
}

// buildListNodesSQL 构建完整的 SQL 查询
func (s *PostgresGraphStore) buildListNodesSQL(query GraphNodeQuery) (string, []interface{}) {
	whereClause, args, argIndex := buildWhereClause(query)
	orderBy := buildOrderByClause(query)
	limitClause, offsetClause, paginationArgs := buildPaginationClauses(query, argIndex)
	args = append(args, paginationArgs...)

	sqlQuery := fmt.Sprintf(`
		SELECT
			id, kind, content, state, version,
			metadata, created_at, updated_at
		FROM %s
		%s
		ORDER BY %s
		%s %s
	`, s.nodeTable, whereClause, orderBy, limitClause, offsetClause)

	return sqlQuery, args
}

// scanGraphNode 扫描一行数据到 GraphNode
func scanGraphNode(rows pgx.Rows) (*GraphNode, error) {
	var node GraphNode
	var state *string
	var metadataJSON []byte

	err := rows.Scan(
		&node.ID,
		&node.Kind,
		&node.Content,
		&state,
		&node.Version,
		&metadataJSON,
		&node.CreatedAt,
		&node.UpdatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("scan node: %w", err)
	}

	// 解析 metadata
	if len(metadataJSON) > 0 {
		if err := json.Unmarshal(metadataJSON, &node.Metadata); err != nil {
			return nil, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}
	if node.Metadata == nil {
		node.Metadata = make(map[string]interface{})
	}

	if state != nil {
		node.State = *state
	}

	return &node, nil
}

// executeListNodesQuery 执行查询并扫描结果
func executeListNodesQuery(ctx context.Context, pool *pgxpool.Pool, sqlQuery string, args []interface{}) ([]*GraphNode, error) {
	rows, err := pool.Query(ctx, sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("query nodes: %w", err)
	}
	defer rows.Close()

	var nodes []*GraphNode
	for rows.Next() {
		node, err := scanGraphNode(rows)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate nodes: %w", err)
	}

	return nodes, nil
}
