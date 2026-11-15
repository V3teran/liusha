package explorationgraph

import (
	"encoding/json"

	"github.com/V3teran/liusha/internal/framework/core"
)

// ─────────────────────────────────────────────
// 辅助转换
// ─────────────────────────────────────────────

// convertGraphNodesToNodes 转换 GraphNode 列表为 Node 列表
func (s *Store) convertGraphNodesToNodes(graphNodes []*core.GraphNode) []Node {
	nodes := make([]Node, 0, len(graphNodes))
	for _, gn := range graphNodes {
		nodes = append(nodes, *s.graphNodeToNode(gn))
	}
	return nodes
}

// graphNodeToNode 通用图节点 → 领域节点。类型化字段从 metadata 恢复，
// 其余 metadata 键序列化进 Node.Metadata（业务扩展位）。
func (s *Store) graphNodeToNode(graphNode *core.GraphNode) *Node {
	node := &Node{
		ID:        graphNode.ID,
		Kind:      core.NodeKind(graphNode.Kind),
		Content:   graphNode.Content,
		CreatedAt: graphNode.CreatedAt,
		UpdatedAt: graphNode.UpdatedAt,
		Version:   graphNode.Version,
	}

	// 从 Metadata 恢复类型化字段
	node.TaskID = graphNode.TaskID
	if sourceType, ok := graphNode.Metadata["source_type"].(string); ok {
		node.SourceType = SourceType(sourceType)
	}
	if priority, ok := graphNode.Metadata["priority"].(string); ok {
		node.Priority = Priority(priority)
	}

	// State
	if graphNode.State != "" {
		state := State(graphNode.State)
		node.State = &state
	}

	// Complexity
	if complexity, ok := graphNode.Metadata["complexity"].(string); ok {
		c := Complexity(complexity)
		node.Complexity = &c
	}

	// StateReason
	if stateReason, ok := graphNode.Metadata["state_reason"].(string); ok {
		node.StateReason = &stateReason
	}

	// 验证状态（仅假设观察）
	if status, ok := graphNode.Metadata[metaVerification].(string); ok {
		v := VerificationStatus(status)
		node.VerificationStatus = &v
	}

	// 其余键 → Node.Metadata（refute_reason / failure_reason 等业务扩展位）
	leftover := make(map[string]interface{}, len(graphNode.Metadata))
	for k, v := range graphNode.Metadata {
		if !typedMetadataKeys[k] {
			leftover[k] = v
		}
	}
	if len(leftover) > 0 {
		if b, err := json.Marshal(leftover); err == nil {
			node.Metadata = b
		}
	}

	return node
}

// convertGraphEdgesToEdges 转换 GraphEdge 列表为 Edge 列表
func (s *Store) convertGraphEdgesToEdges(graphEdges []*core.GraphEdge, taskID string) []Edge {
	edges := make([]Edge, 0, len(graphEdges))
	for _, ge := range graphEdges {
		edge := Edge{
			TaskID:    taskID,
			SrcID:     ge.From,
			Rel:       Relation(ge.Relation),
			DstID:     ge.To,
			CreatedAt: ge.CreatedAt,
		}

		edges = append(edges, edge)
	}
	return edges
}
