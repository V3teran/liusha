package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// Traverse 图遍历（BFS/DFS）。
func (s *PostgresGraphStore) Traverse(ctx context.Context, startID string, query GraphTraverseQuery) ([]*GraphNode, error) {
	if startID == "" {
		return nil, errors.New("start node ID cannot be empty")
	}

	// 设置默认值
	if query.Direction == "" {
		query.Direction = GraphTraverseOut
	}
	if query.Strategy == "" {
		query.Strategy = GraphTraverseBFS
	}

	// 使用迭代实现遍历（避免递归深度限制）
	visited := make(map[string]bool)
	var result []*GraphNode
	queue := []string{startID}

	depth := 0
	for len(queue) > 0 {
		// 检查深度限制
		if query.MaxDepth > 0 && depth >= query.MaxDepth {
			break
		}

		// BFS: 取队列头；DFS: 取队列尾
		var currentID string
		if query.Strategy == GraphTraverseBFS {
			currentID = queue[0]
			queue = queue[1:]
		} else {
			currentID = queue[len(queue)-1]
			queue = queue[:len(queue)-1]
		}

		// 跳过已访问节点
		if visited[currentID] {
			continue
		}
		visited[currentID] = true

		// 获取当前节点
		node, err := s.GetNode(ctx, currentID)
		if err != nil {
			if errors.Is(err, ErrGraphNodeNotFound) {
				continue
			}
			return nil, fmt.Errorf("get node %s: %w", currentID, err)
		}

		// 应用节点过滤器
		if query.NodeFilter != nil {
			if !s.matchNodeFilter(node, *query.NodeFilter) {
				continue
			}
		}

		result = append(result, node)

		// 获取邻居节点
		neighbors, err := s.getNeighbors(ctx, currentID, query.Direction, query.Relations)
		if err != nil {
			return nil, fmt.Errorf("get neighbors of %s: %w", currentID, err)
		}

		// 添加到队列
		queue = append(queue, neighbors...)

		depth++
	}

	return result, nil
}

// getNeighbors 获取节点的邻居。
func (s *PostgresGraphStore) getNeighbors(ctx context.Context, nodeID string, direction GraphTraverseDirection, relations []string) ([]string, error) {
	var query string
	var args []interface{}

	// 构建关系过滤条件
	relFilter := ""
	if len(relations) > 0 {
		relPlaceholders := make([]string, len(relations))
		argOffset := 2 // 从 $2 开始
		for i := range relations {
			relPlaceholders[i] = fmt.Sprintf("$%d", argOffset+i)
		}
		relFilter = fmt.Sprintf(" AND rel IN (%s)", strings.Join(relPlaceholders, ","))
	}

	switch direction {
	case GraphTraverseOut:
		query = fmt.Sprintf("SELECT dst_id FROM "+s.edgeTable+" WHERE src_id = $1%s", relFilter)
		args = []interface{}{nodeID}
	case GraphTraverseIn:
		query = fmt.Sprintf("SELECT src_id FROM "+s.edgeTable+" WHERE dst_id = $1%s", relFilter)
		args = []interface{}{nodeID}
	case GraphTraverseBoth:
		query = fmt.Sprintf(`
			SELECT dst_id FROM `+s.edgeTable+` WHERE src_id = $1%s
			UNION
			SELECT src_id FROM `+s.edgeTable+` WHERE dst_id = $2%s
		`, relFilter, relFilter)
		args = []interface{}{nodeID, nodeID}
	default:
		return nil, fmt.Errorf("invalid traverse direction: %s", direction)
	}

	// 添加关系过滤参数
	if len(relations) > 0 {
		for _, rel := range relations {
			args = append(args, rel)
		}
		// 对于 GraphTraverseBoth，需要再添加一次参数
		if direction == GraphTraverseBoth {
			for _, rel := range relations {
				args = append(args, rel)
			}
		}
	}

	rows, err := s.pool.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query neighbors: %w", err)
	}
	defer rows.Close()

	var neighbors []string
	for rows.Next() {
		var neighborID string
		if err := rows.Scan(&neighborID); err != nil {
			return nil, fmt.Errorf("scan neighbor: %w", err)
		}
		neighbors = append(neighbors, neighborID)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate neighbors: %w", err)
	}

	return neighbors, nil
}

// matchNodeFilter 检查节点是否匹配过滤器。
func (s *PostgresGraphStore) matchNodeFilter(node *GraphNode, filter GraphNodeQuery) bool {
	if filter.Kind != "" && node.Kind != filter.Kind {
		return false
	}
	if filter.State != "" && node.State != filter.State {
		return false
	}
	if filter.MinConfidence > 0 && node.Confidence < filter.MinConfidence {
		return false
	}
	return true
}

// getNodeTaskID 获取节点的 task_id。
func (s *PostgresGraphStore) getNodeTaskID(ctx context.Context, nodeID string) (string, error) {
	var taskID string
	err := s.pool.QueryRow(ctx, "SELECT task_id FROM "+s.nodeTable+" WHERE id = $1", nodeID).Scan(&taskID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrGraphNodeNotFound
		}
		return "", err
	}
	return taskID, nil
}
