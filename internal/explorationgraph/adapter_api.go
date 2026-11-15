package explorationgraph

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// ─────────────────────────────────────────────
// HTTP API 专用方法
// ─────────────────────────────────────────────

// ListNodesByTask 按 task_id 查询节点，kind 非空时只取该类型（按创建时间升序）。
func (s *Store) ListNodesByTask(ctx context.Context, taskID string, kind string) ([]Node, error) {
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

// ListEdgesByTask 按 task_id 查询边。
// 边查询按 task_id 列过滤（有索引），不做全表拉取后内存过滤。
func (s *Store) ListEdgesByTask(ctx context.Context, taskID string) ([]Edge, error) {
	graphEdges, err := s.graphStore.ListEdges(ctx, core.GraphEdgeQuery{TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
	}

	return s.convertGraphEdgesToEdges(graphEdges, taskID), nil
}
