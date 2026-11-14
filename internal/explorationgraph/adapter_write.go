package explorationgraph

import (
	"context"
	"encoding/json"

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
	metadata := map[string]interface{}{}
	// 空值不写入 metadata：所有创建点都显式声明来源；无「系统默认来源」语义。
	if node.SourceType != "" {
		metadata["source_type"] = string(node.SourceType)
	}
	if node.Priority != "" {
		metadata["priority"] = string(node.Priority)
	}

	graphNode := &core.GraphNode{
		ID:        node.ID,
		TaskID:    node.TaskID,
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
	if node.StateReason != nil {
		graphNode.Metadata["state_reason"] = *node.StateReason
	}

	// 验证状态（仅假设观察），枚举原文入库。
	if node.VerificationStatus != nil {
		graphNode.Metadata[metaVerification] = string(*node.VerificationStatus)
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
