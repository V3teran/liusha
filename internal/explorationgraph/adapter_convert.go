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
	}

	// 从 Metadata 恢复类型化字段
	if taskID, ok := graphNode.Metadata["task_id"].(string); ok {
		node.TaskID = taskID
	}
	if sourceType, ok := graphNode.Metadata["source_type"].(string); ok {
		node.SourceType = SourceType(sourceType)
	}
	if sourceID, ok := graphNode.Metadata["source_id"].(string); ok {
		node.SourceID = sourceID
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

	// DependsOn（内存后端存 []string，JSON 往返后是 []interface{}，两者都接受）
	switch dependsOn := graphNode.Metadata["depends_on"].(type) {
	case []interface{}:
		deps := make([]string, 0, len(dependsOn))
		for _, dep := range dependsOn {
			if str, ok := dep.(string); ok {
				deps = append(deps, str)
			}
		}
		node.DependsOn = deps
	case []string:
		node.DependsOn = dependsOn
	}

	// BlockedReason
	if blockedReason, ok := graphNode.Metadata["blocked_reason"].(string); ok {
		node.BlockedReason = &blockedReason
	}

	// Confidence：验证生命周期裁决，枚举原文存取
	if confidence, ok := graphNode.Metadata["confidence"].(string); ok {
		c := Confidence(confidence)
		node.Confidence = &c
	}

	// 其余键 → Node.Metadata（verification_outcome / failure_reason 等业务扩展位）
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
