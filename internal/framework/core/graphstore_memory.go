package core

import (
	"context"
	"sync"
	"time"
)

// MemoryGraphStore 是 GraphStore 的内存实现（用于测试和 PoC）
type MemoryGraphStore struct {
	nodes map[string]*GraphNode
	edges map[string]*GraphEdge // key: "from:to:relation"
	mu    sync.RWMutex
}

// NewMemoryGraphStore 创建内存图存储
func NewMemoryGraphStore() *MemoryGraphStore {
	return &MemoryGraphStore{
		nodes: make(map[string]*GraphNode),
		edges: make(map[string]*GraphEdge),
	}
}

// CreateNode 创建节点
func (m *MemoryGraphStore) CreateNode(ctx context.Context, node *GraphNode) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 复制节点（避免外部修改）
	nodeCopy := *node
	if nodeCopy.CreatedAt.IsZero() {
		nodeCopy.CreatedAt = time.Now()
	}
	nodeCopy.UpdatedAt = time.Now()

	m.nodes[node.ID] = &nodeCopy
	return nil
}

// GetNode 获取节点
func (m *MemoryGraphStore) GetNode(ctx context.Context, id string) (*GraphNode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, ok := m.nodes[id]
	if !ok {
		return nil, ErrGraphNodeNotFound
	}

	// 返回副本
	nodeCopy := *node
	return &nodeCopy, nil
}

// UpdateNode 更新节点
func (m *MemoryGraphStore) UpdateNode(ctx context.Context, id string, update GraphNodeUpdate) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	node, ok := m.nodes[id]
	if !ok {
		return ErrGraphNodeNotFound
	}

	// 应用更新
	if update.Content != nil {
		node.Content = update.Content
	}
	if update.State != "" {
		node.State = update.State
	}
	if update.Confidence != nil {
		node.Confidence = *update.Confidence
	}
	if update.Metadata != nil {
		node.Metadata = update.Metadata
	}

	node.UpdatedAt = time.Now()
	return nil
}

// DeleteNode 删除节点
func (m *MemoryGraphStore) DeleteNode(ctx context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, ok := m.nodes[id]; !ok {
		return ErrGraphNodeNotFound
	}

	// 删除节点
	delete(m.nodes, id)

	// 删除相关的边
	for key, edge := range m.edges {
		if edge.From == id || edge.To == id {
			delete(m.edges, key)
		}
	}

	return nil
}

// ListNodes 查询节点列表
func (m *MemoryGraphStore) ListNodes(ctx context.Context, query GraphNodeQuery) ([]*GraphNode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*GraphNode

	for _, node := range m.nodes {
		// 应用过滤条件
		if query.Kind != "" && node.Kind != query.Kind {
			continue
		}

		if query.State != "" && node.State != query.State {
			continue
		}

		if query.MinConfidence > 0 && node.Confidence < query.MinConfidence {
			continue
		}

		// Filters（metadata 过滤）
		match := true
		for key, value := range query.Filters {
			if node.Metadata == nil {
				match = false
				break
			}
			if nodeValue, ok := node.Metadata[key]; !ok || nodeValue != value {
				match = false
				break
			}
		}

		if !match {
			continue
		}

		// 添加到结果
		nodeCopy := *node
		result = append(result, &nodeCopy)
	}

	// 分页
	if query.Offset > 0 && query.Offset < len(result) {
		result = result[query.Offset:]
	}

	if query.Limit > 0 && query.Limit < len(result) {
		result = result[:query.Limit]
	}

	return result, nil
}

// CreateEdge 创建边
func (m *MemoryGraphStore) CreateEdge(ctx context.Context, edge *GraphEdge) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查节点是否存在
	if _, ok := m.nodes[edge.From]; !ok {
		return ErrGraphNodeNotFound
	}
	if _, ok := m.nodes[edge.To]; !ok {
		return ErrGraphNodeNotFound
	}

	// 复制边
	edgeCopy := *edge
	if edgeCopy.CreatedAt.IsZero() {
		edgeCopy.CreatedAt = time.Now()
	}

	key := edgeKey(edge.From, edge.To, edge.Relation)
	m.edges[key] = &edgeCopy

	return nil
}

// ListEdges 查询边列表
func (m *MemoryGraphStore) ListEdges(ctx context.Context, query GraphEdgeQuery) ([]*GraphEdge, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var result []*GraphEdge

	for _, edge := range m.edges {
		// 应用过滤条件
		if query.From != "" && edge.From != query.From {
			continue
		}

		if query.To != "" && edge.To != query.To {
			continue
		}

		if query.Relation != "" && edge.Relation != query.Relation {
			continue
		}

		// 添加到结果
		edgeCopy := *edge
		result = append(result, &edgeCopy)
	}

	// 分页
	if query.Limit > 0 && query.Limit < len(result) {
		result = result[:query.Limit]
	}

	return result, nil
}

// DeleteEdge 删除边
func (m *MemoryGraphStore) DeleteEdge(ctx context.Context, from, to, relation string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := edgeKey(from, to, relation)
	if _, ok := m.edges[key]; !ok {
		return ErrGraphEdgeNotFound
	}

	delete(m.edges, key)
	return nil
}

// Traverse 图遍历
func (m *MemoryGraphStore) Traverse(ctx context.Context, startID string, query GraphTraverseQuery) ([]*GraphNode, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// 检查起始节点是否存在
	startNode, ok := m.nodes[startID]
	if !ok {
		return nil, ErrGraphNodeNotFound
	}

	visited := make(map[string]bool)
	var result []*GraphNode

	// BFS 遍历（默认）
	if query.Strategy == GraphTraverseBFS || query.Strategy == "" {
		queue := []*GraphNode{startNode}
		depth := 0

		for len(queue) > 0 {
			// 先检查深度限制（在处理当前层之前）
			if query.MaxDepth > 0 && depth > query.MaxDepth {
				break
			}

			levelSize := len(queue)

			for i := 0; i < levelSize; i++ {
				current := queue[0]
				queue = queue[1:]

				if visited[current.ID] {
					continue
				}
				visited[current.ID] = true

				// 应用节点过滤
				if query.NodeFilter == nil || m.matchNodeFilter(current, query.NodeFilter) {
					nodeCopy := *current
					result = append(result, &nodeCopy)
				}

				// 只在深度允许时获取邻居（下一层的深度是 depth+1）
				if query.MaxDepth == 0 || depth < query.MaxDepth {
					neighbors := m.getNeighbors(current.ID, query.Direction, query.Relations)
					queue = append(queue, neighbors...)
				}
			}

			depth++
		}
	} else {
		// DFS 遍历
		m.dfsTraverse(startNode, query, visited, &result, 0)
	}

	return result, nil
}

// getNeighbors 获取邻居节点
func (m *MemoryGraphStore) getNeighbors(nodeID string, direction GraphTraverseDirection, relations []string) []*GraphNode {
	var neighbors []*GraphNode

	for _, edge := range m.edges {
		// 检查关系类型过滤
		if len(relations) > 0 {
			match := false
			for _, rel := range relations {
				if edge.Relation == rel {
					match = true
					break
				}
			}
			if !match {
				continue
			}
		}

		// 根据方向获取邻居
		var neighborID string
		switch direction {
		case GraphTraverseOut, "":
			if edge.From == nodeID {
				neighborID = edge.To
			}
		case GraphTraverseIn:
			if edge.To == nodeID {
				neighborID = edge.From
			}
		case GraphTraverseBoth:
			if edge.From == nodeID {
				neighborID = edge.To
			} else if edge.To == nodeID {
				neighborID = edge.From
			}
		}

		if neighborID != "" {
			if node, ok := m.nodes[neighborID]; ok {
				neighbors = append(neighbors, node)
			}
		}
	}

	return neighbors
}

// dfsTraverse DFS 遍历
func (m *MemoryGraphStore) dfsTraverse(node *GraphNode, query GraphTraverseQuery, visited map[string]bool, result *[]*GraphNode, depth int) {
	if visited[node.ID] {
		return
	}

	if query.MaxDepth > 0 && depth >= query.MaxDepth {
		return
	}

	visited[node.ID] = true

	if query.NodeFilter == nil || m.matchNodeFilter(node, query.NodeFilter) {
		nodeCopy := *node
		*result = append(*result, &nodeCopy)
	}

	neighbors := m.getNeighbors(node.ID, query.Direction, query.Relations)
	for _, neighbor := range neighbors {
		m.dfsTraverse(neighbor, query, visited, result, depth+1)
	}
}

// matchNodeFilter 检查节点是否匹配过滤条件
func (m *MemoryGraphStore) matchNodeFilter(node *GraphNode, filter *GraphNodeQuery) bool {
	if filter.Kind != "" && node.Kind != filter.Kind {
		return false
	}

	if filter.State != "" && node.State != filter.State {
		return false
	}

	if filter.MinConfidence > 0 && node.Confidence < filter.MinConfidence {
		return false
	}

	for key, value := range filter.Filters {
		if node.Metadata == nil {
			return false
		}
		if nodeValue, ok := node.Metadata[key]; !ok || nodeValue != value {
			return false
		}
	}

	return true
}

// edgeKey 生成边的唯一键
func edgeKey(from, to, relation string) string {
	return from + ":" + to + ":" + relation
}
