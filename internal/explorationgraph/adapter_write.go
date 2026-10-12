package explorationgraph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// ─────────────────────────────────────────────
// 写入
// ─────────────────────────────────────────────

// CreateNode 创建节点。
func (s *Store) CreateNode(ctx context.Context, node Node) (string, error) {
	if err := s.graphStore.CreateNode(ctx, s.nodeToGraphNode(node)); err != nil {
		return "", err
	}
	return node.ID, nil
}

// CreateNodeWithEdges 在单个事务内创建节点及其关联边——图上"节点 + 溯源边"
// 是一个不可分的事实，部分写入会产生失去归属的孤立节点。
func (s *Store) CreateNodeWithEdges(ctx context.Context, node Node, edges []Edge) error {
	graphEdges := make([]*core.GraphEdge, 0, len(edges))
	for i := range edges {
		graphEdges = append(graphEdges, s.edgeToGraphEdge(&edges[i]))
	}
	return s.graphStore.CreateNodeWithEdges(ctx, s.nodeToGraphNode(node), graphEdges)
}

// CreateBusinessEdge 创建边。
func (s *Store) CreateBusinessEdge(ctx context.Context, edge Edge) error {
	return s.graphStore.CreateEdge(ctx, s.edgeToGraphEdge(&edge))
}

// nodeToGraphNode 领域节点 → 通用图节点。
func (s *Store) nodeToGraphNode(node Node) *core.GraphNode {
	metadata := map[string]interface{}{
		"task_id": node.TaskID,
	}
	// 空值不写入 metadata：让 GraphStore 落回默认值（source_type=system 等）。
	if node.SourceType != "" {
		metadata["source_type"] = string(node.SourceType)
	}
	if node.SourceID != "" {
		metadata["source_id"] = node.SourceID
	}
	if node.Priority != "" {
		metadata["priority"] = node.Priority
	}

	graphNode := &core.GraphNode{
		ID:        node.ID,
		Kind:      string(node.Kind),
		Content:   node.Content,
		Metadata:  metadata,
		CreatedAt: node.CreatedAt,
		UpdatedAt: node.UpdatedAt,
	}

	// Action 专用字段
	if node.State != nil {
		graphNode.State = string(*node.State)
	} else if node.Kind == core.KindAction {
		// Action 节点必须有 state，默认为 open
		graphNode.State = string(StateOpen)
	}
	if node.Complexity != nil {
		graphNode.Metadata["complexity"] = string(*node.Complexity)
	}
	if len(node.DependsOn) > 0 {
		graphNode.Metadata["depends_on"] = node.DependsOn
	}
	if node.BlockedReason != nil {
		graphNode.Metadata["blocked_reason"] = *node.BlockedReason
	}

	// Confidence：验证生命周期裁决（unverified/verified/refuted），枚举原文入库。
	// executor 的自评置信度（low/medium/high）属于假设载荷，走 Content，不进此处。
	if node.Confidence != nil {
		graphNode.Metadata["confidence"] = string(*node.Confidence)
	}

	// 领域 Metadata 平铺合并（键不与类型化键冲突时等价于直接落库）
	if len(node.Metadata) > 0 {
		var extra map[string]interface{}
		if json.Unmarshal(node.Metadata, &extra) == nil {
			for k, v := range extra {
				graphNode.Metadata[k] = v
			}
		}
	}

	return graphNode
}

// edgeToGraphEdge 领域边 → 通用图边。
func (s *Store) edgeToGraphEdge(edge *Edge) *core.GraphEdge {
	graphEdge := &core.GraphEdge{
		TaskID:    edge.TaskID,
		From:      edge.SrcID,
		To:        edge.DstID,
		Relation:  string(edge.Rel),
		Metadata:  make(map[string]interface{}),
		CreatedAt: edge.CreatedAt,
	}

	if len(edge.Attrs) > 0 {
		var attrs map[string]interface{}
		if err := json.Unmarshal(edge.Attrs, &attrs); err == nil {
			graphEdge.Metadata = attrs
		}
	}

	return graphEdge
}

// UpdateNodeContent 更新节点内容（控制平面 adjust_goal 等操作使用）。
func (s *Store) UpdateNodeContent(ctx context.Context, id string, content json.RawMessage) error {
	return s.graphStore.UpdateNode(ctx, id, core.GraphNodeUpdate{Content: content})
}

// UpdateActionStateWithReason 更新 action 状态和阻塞原因（单条 UPDATE，原子生效）。
// aborted 是终态，不允许覆盖（除非目标状态也是 aborted）。
func (s *Store) UpdateActionStateWithReason(ctx context.Context, id string, state State, blockedReason *string) error {
	// 先检查当前状态（防止覆盖 aborted）
	current, err := s.GetNode(ctx, id)
	if err != nil {
		return fmt.Errorf("get current state before update: %w", err)
	}

	// aborted 是终态，不允许覆盖（除非目标状态也是 aborted）
	if current.State != nil && *current.State == StateAborted && state != StateAborted {
		return fmt.Errorf("cannot overwrite aborted state (current: aborted, target: %s)", state)
	}

	update := core.GraphNodeUpdate{
		State: string(state),
	}

	if blockedReason != nil {
		update.Metadata = map[string]interface{}{
			"blocked_reason": *blockedReason,
		}
	}

	return s.graphStore.UpdateNode(ctx, id, update)
}

// CompareAndSwapActionState 原子性地比较并交换 action 状态。
// 只有当前状态等于 expectedState 时才更新为 newState，用于执行方抢占认领。
// 返回 (true, nil) 表示更新成功；(false, nil) 表示状态不匹配（已被抢占/已流转）。
func (s *Store) CompareAndSwapActionState(ctx context.Context, taskID, actionID string, expectedState, newState State) (bool, error) {
	return s.graphStore.CompareAndSwapState(ctx, taskID, actionID, string(expectedState), string(newState))
}

