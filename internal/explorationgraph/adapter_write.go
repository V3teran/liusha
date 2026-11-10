package explorationgraph

import (
	"context"
	"encoding/json"
	"errors"
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

// nodeToGraphNode 领域节点 → 通用图节点。
func (s *Store) nodeToGraphNode(node Node) *core.GraphNode {
	metadata := map[string]interface{}{
		"task_id": node.TaskID,
	}
	// 空值不写入 metadata：所有创建点都显式声明来源；无「系统默认来源」语义。
	if node.SourceType != "" {
		metadata["source_type"] = string(node.SourceType)
	}
	if node.SourceID != "" {
		metadata["source_id"] = node.SourceID
	}
	if node.Priority != "" {
		metadata["priority"] = string(node.Priority)
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

	return graphEdge
}

// UpdateNodeContent 更新节点内容（控制平面 adjust_goal 等操作使用）。
func (s *Store) UpdateNodeContent(ctx context.Context, id string, content json.RawMessage) error {
	return s.graphStore.UpdateNode(ctx, id, core.GraphNodeUpdate{Content: content})
}

// TransitionState 迁移节点状态（action/objective 通用）并可选附带原因。
// 状态以读到的当前状态为 CAS 前置，与原因（metadata.blocked_reason，依赖阻塞 /
// kill 与判死理由等）在同一事务内落定——读-判-写窗口内若被并发迁移，CAS 前置
// 不匹配即整体失败，不会覆盖并发写入的终态，也不会留下「原因已写、状态未变」。
// aborted 是终态，不允许覆盖（除非目标状态也是 aborted）。superseded 不经此
// 入口进出：它与 supersedes 边是同一事实，只能由 RevisePlan 连边一起原子写入，
// 且一经写入永不迁出。
func (s *Store) TransitionState(ctx context.Context, id string, state State, reason *string) error {
	if state == StateSuperseded {
		return fmt.Errorf("superseded 只能经 RevisePlan 与 supersedes 边一同写入（node %s）", id)
	}

	current, err := s.GetNode(ctx, id)
	if err != nil {
		return fmt.Errorf("get current state before update: %w", err)
	}
	if current.State == nil {
		return fmt.Errorf("节点 %s（%s）没有状态机，不能迁移状态", id, current.Kind)
	}
	if *current.State == StateAborted && state != StateAborted {
		return fmt.Errorf("cannot overwrite aborted state (current: aborted, target: %s)", state)
	}
	if *current.State == StateSuperseded {
		return fmt.Errorf("cannot overwrite superseded state (current: superseded, target: %s)", state)
	}

	changes := core.GraphChangeSet{
		Transitions: []core.GraphStateTransition{{
			TaskID: current.TaskID, NodeID: id, From: string(*current.State), To: string(state),
		}},
	}
	if reason != nil {
		changes.Annotations = []core.GraphNodeAnnotation{{
			TaskID: current.TaskID, NodeID: id, Metadata: map[string]interface{}{"blocked_reason": *reason},
		}}
	}
	if err := s.graphStore.ApplyChangeSet(ctx, changes); err != nil {
		if errors.Is(err, core.ErrGraphStateConflict) {
			return fmt.Errorf("state changed concurrently (node %s expected state %s): %w", id, *current.State, err)
		}
		return fmt.Errorf("transition node %s: %w", id, err)
	}
	return nil
}

// CompareAndSwapActionState 原子性地比较并交换 action 状态。
// 只有当前状态等于 expectedState 时才更新为 newState，用于执行方抢占认领。
// 返回 (true, nil) 表示更新成功；(false, nil) 表示状态不匹配（已被抢占/已流转）。
func (s *Store) CompareAndSwapActionState(ctx context.Context, taskID, actionID string, expectedState, newState State) (bool, error) {
	return s.graphStore.CompareAndSwapState(ctx, taskID, actionID, string(expectedState), string(newState))
}
