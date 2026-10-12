package explorationgraph

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// ─────────────────────────────────────────────
// HTTP API 专用方法
// ─────────────────────────────────────────────

// ListNodesForAPI 按 task_id 和可选 kind 查询节点（HTTP API 专用）
func (s *Store) ListNodesForAPI(ctx context.Context, taskID string, kind string) ([]Node, error) {
	query := core.GraphNodeQuery{
		Filters: map[string]interface{}{
			"task_id": taskID,
		},
		OrderBy: "created_at",
	}

	// 可选的 kind 过滤
	if kind != "" {
		query.Kind = kind
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	return s.convertGraphNodesToNodes(nodes), nil
}

// ListEdgesForAPI 按 task_id 查询边（HTTP API 专用）。
// 边查询按 task_id 列过滤（有索引），不做全表拉取后内存过滤。
func (s *Store) ListEdgesForAPI(ctx context.Context, taskID string) ([]Edge, error) {
	graphEdges, err := s.graphStore.ListEdges(ctx, core.GraphEdgeQuery{TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
	}

	return s.convertGraphEdgesToEdges(graphEdges, taskID), nil
}

// GetStatsForAPI 返回节点类型统计（e2e 轮询专用）。
// 聚合下推到数据库（GROUP BY），不拉取节点载荷。
func (s *Store) GetStatsForAPI(ctx context.Context, taskID string) (map[string]int, error) {
	stats := map[string]int{
		"objectives":   0,
		"actions":      0,
		"observations": 0,
		"results":      0,
	}

	if s.pool != nil {
		rows, err := s.pool.Query(ctx, `
			SELECT kind, COUNT(*) FROM exploration_node
			WHERE task_id = $1
			GROUP BY kind
		`, taskID)
		if err != nil {
			return nil, fmt.Errorf("query node stats: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var kind string
			var count int
			if err := rows.Scan(&kind, &count); err != nil {
				return nil, fmt.Errorf("scan node stats: %w", err)
			}
			if _, ok := stats[kind]; ok {
				stats[kind] = count
			}
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate node stats: %w", err)
		}
		return stats, nil
	}

	// 内存后端：通用查询后在内存计数
	nodes, err := s.ListNodesForAPI(ctx, taskID, "")
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		switch node.Kind {
		case core.KindObjective:
			stats["objectives"]++
		case core.KindAction:
			stats["actions"]++
		case core.KindObservation:
			stats["observations"]++
		case core.KindResult:
			stats["results"]++
		}
	}
	return stats, nil
}

// RecoverOrphanedActions 恢复孤儿 action（runner 重启时调用）
//
// 将所有处于 running 状态的 action 重置为 failed，原因是 runner 重启导致执行中断。
// 这防止了 action 永远停留在 running 状态而无法被重新调度。
func (s *Store) RecoverOrphanedActions(ctx context.Context) (int, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("RecoverOrphanedActions requires direct DB access (pool is nil)")
	}

	query := `
		UPDATE exploration_node
		SET
			state = 'failed',
			metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object(
				'failure_reason', 'Runner 重启导致执行中断',
				'recovered_at', NOW()
			)
		WHERE kind = 'action' AND state = 'running'
		RETURNING id
	`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("update orphaned actions: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return count, fmt.Errorf("scan recovered action id: %w", err)
		}
		count++
	}

	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("iterate recovered actions: %w", err)
	}

	return count, nil
}
