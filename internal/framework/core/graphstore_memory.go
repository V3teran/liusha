package core

import (
	"context"
	"fmt"
	"sync"
)

// InMemoryGraphStore 内存实现的 GraphStore（用于测试）
type InMemoryGraphStore struct {
	mu    sync.RWMutex
	nodes map[string]*GraphNode
	edges []*GraphEdge
}

// NewInMemoryGraphStore 创建内存 GraphStore
func NewInMemoryGraphStore() GraphStore {
	return &InMemoryGraphStore{
		nodes: make(map[string]*GraphNode),
		edges: make([]*GraphEdge, 0),
	}
}

// CreateNode 创建节点
func (s *InMemoryGraphStore) CreateNode(ctx context.Context, node *GraphNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.nodes[node.ID]; exists {
		return fmt.Errorf("node %s already exists", node.ID)
	}

	// 深拷贝
	nodeCopy := &GraphNode{
		ID:         node.ID,
		Kind:       node.Kind,
		Content:    append([]byte(nil), node.Content...),
		State:      node.State,
		Confidence: node.Confidence,
		Version:    node.Version,
		Metadata:   make(map[string]interface{}),
		CreatedAt:  node.CreatedAt,
		UpdatedAt:  node.UpdatedAt,
	}

	// Version 默认为 1
	if nodeCopy.Version == 0 {
		nodeCopy.Version = 1
	}

	for k, v := range node.Metadata {
		nodeCopy.Metadata[k] = v
	}

	s.nodes[node.ID] = nodeCopy
	return nil
}

// GetNode 获取节点
func (s *InMemoryGraphStore) GetNode(ctx context.Context, id string) (*GraphNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node, exists := s.nodes[id]
	if !exists {
		return nil, ErrGraphNodeNotFound
	}

	// 深拷贝
	nodeCopy := &GraphNode{
		ID:         node.ID,
		Kind:       node.Kind,
		Content:    append([]byte(nil), node.Content...),
		State:      node.State,
		Confidence: node.Confidence,
		Version:    node.Version,
		Metadata:   make(map[string]interface{}),
		CreatedAt:  node.CreatedAt,
		UpdatedAt:  node.UpdatedAt,
	}

	for k, v := range node.Metadata {
		nodeCopy.Metadata[k] = v
	}

	return nodeCopy, nil
}

// UpdateNode 更新节点
func (s *InMemoryGraphStore) UpdateNode(ctx context.Context, id string, update GraphNodeUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[id]
	if !exists {
		return ErrGraphNodeNotFound
	}

	// 乐观锁检查
	if update.ExpectedVersion != nil {
		if node.Version != *update.ExpectedVersion {
			return ErrVersionMismatch
		}
	}

	if update.Content != nil {
		node.Content = append([]byte(nil), update.Content...)
	}

	if update.State != "" {
		node.State = update.State
	}

	if update.Confidence != nil {
		node.Confidence = *update.Confidence
	}

	if update.Metadata != nil {
		for k, v := range update.Metadata {
			node.Metadata[k] = v
		}
	}

	// 更新后递增 version
	node.Version++

	return nil
}

// CompareAndSwapState 原子更新节点状态（使用乐观锁）
func (s *InMemoryGraphStore) CompareAndSwapState(ctx context.Context, taskID, nodeID string, expectedState, newState string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[nodeID]
	if !exists {
		return false, ErrGraphNodeNotFound
	}

	// 检查 taskID（如果提供）
	if taskID != "" {
		// 从 metadata 中获取 task_id
		if node.Metadata != nil {
			if tid, ok := node.Metadata["task_id"].(string); ok && tid != taskID {
				return false, nil // 不属于该任务
			}
		}
	}

	// 检查状态是否匹配
	if node.State != expectedState {
		// 状态不匹配，CAS 失败
		return false, nil
	}

	// 状态匹配，更新状态并递增 version
	node.State = newState
	node.Version++

	return true, nil
}

// DeleteNode 删除节点
func (s *InMemoryGraphStore) DeleteNode(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.nodes[id]; !exists {
		return fmt.Errorf("node %s not found", id)
	}

	delete(s.nodes, id)

	// 删除相关边
	newEdges := make([]*GraphEdge, 0)
	for _, edge := range s.edges {
		if edge.From != id && edge.To != id {
			newEdges = append(newEdges, edge)
		}
	}
	s.edges = newEdges

	return nil
}

// ListNodes 查询节点列表
func (s *InMemoryGraphStore) ListNodes(ctx context.Context, query GraphNodeQuery) ([]*GraphNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*GraphNode, 0)

	for _, node := range s.nodes {
		// 过滤 Kind
		if query.Kind != "" && node.Kind != query.Kind {
			continue
		}

		// 过滤 State
		if query.State != "" && node.State != query.State {
			continue
		}

		// 过滤 Metadata
		match := true
		for k, v := range query.Filters {
			// 支持 "metadata.xxx" 嵌套访问
			if len(k) > 9 && k[:9] == "metadata." {
				metaKey := k[9:]
				if node.Metadata[metaKey] != v {
					match = false
					break
				}
			} else {
				// 直接访问
				if node.Metadata[k] != v {
					match = false
					break
				}
			}
		}

		if !match {
			continue
		}

		// 深拷贝
		nodeCopy := &GraphNode{
			ID:       node.ID,
			Kind:     node.Kind,
			Content:  append([]byte(nil), node.Content...),
			State:    node.State,
			Metadata: make(map[string]interface{}),
		}

		for k, v := range node.Metadata {
			nodeCopy.Metadata[k] = v
		}

		result = append(result, nodeCopy)

		// 限制数量
		if query.Limit > 0 && len(result) >= query.Limit {
			break
		}
	}

	return result, nil
}

// CreateEdge 创建边
func (s *InMemoryGraphStore) CreateEdge(ctx context.Context, edge *GraphEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查节点是否存在
	if _, exists := s.nodes[edge.From]; !exists {
		return fmt.Errorf("from node %s not found", edge.From)
	}

	if _, exists := s.nodes[edge.To]; !exists {
		return fmt.Errorf("to node %s not found", edge.To)
	}

	// 深拷贝
	edgeCopy := &GraphEdge{
		From:     edge.From,
		To:       edge.To,
		Relation: edge.Relation,
		Metadata: make(map[string]interface{}),
	}

	for k, v := range edge.Metadata {
		edgeCopy.Metadata[k] = v
	}

	s.edges = append(s.edges, edgeCopy)
	return nil
}

// ListEdges 查询边列表
func (s *InMemoryGraphStore) ListEdges(ctx context.Context, query GraphEdgeQuery) ([]*GraphEdge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*GraphEdge, 0)

	for _, edge := range s.edges {
		// 过滤 From
		if query.From != "" && edge.From != query.From {
			continue
		}

		// 过滤 To
		if query.To != "" && edge.To != query.To {
			continue
		}

		// 过滤 Relation
		if query.Relation != "" && edge.Relation != query.Relation {
			continue
		}

		// 深拷贝
		edgeCopy := &GraphEdge{
			From:     edge.From,
			To:       edge.To,
			Relation: edge.Relation,
			Metadata: make(map[string]interface{}),
		}

		for k, v := range edge.Metadata {
			edgeCopy.Metadata[k] = v
		}

		result = append(result, edgeCopy)

		// 限制数量
		if query.Limit > 0 && len(result) >= query.Limit {
			break
		}
	}

	return result, nil
}

// DeleteEdge 删除边
func (s *InMemoryGraphStore) DeleteEdge(ctx context.Context, from, to, relation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	found := false
	newEdges := make([]*GraphEdge, 0)

	for _, edge := range s.edges {
		if edge.From == from && edge.To == to && edge.Relation == relation {
			found = true
		} else {
			newEdges = append(newEdges, edge)
		}
	}

	if !found {
		return fmt.Errorf("edge not found")
	}

	s.edges = newEdges
	return nil
}

// Traverse 遍历图
func (s *InMemoryGraphStore) Traverse(ctx context.Context, startID string, query GraphTraverseQuery) ([]*GraphNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if _, exists := s.nodes[startID]; !exists {
		return nil, fmt.Errorf("start node %s not found", startID)
	}

	visited := make(map[string]bool)
	result := make([]*GraphNode, 0)

	var traverse func(nodeID string, depth int)
	traverse = func(nodeID string, depth int) {
		// 检查深度限制
		if query.MaxDepth > 0 && depth > query.MaxDepth {
			return
		}

		// 检查是否已访问
		if visited[nodeID] {
			return
		}

		visited[nodeID] = true
		node := s.nodes[nodeID]

		// 过滤节点（如果有 NodeFilter）
		if query.NodeFilter != nil {
			if query.NodeFilter.Kind != "" && node.Kind != query.NodeFilter.Kind {
				return
			}
			if query.NodeFilter.State != "" && node.State != query.NodeFilter.State {
				return
			}
		}

		// 深拷贝并添加到结果
		nodeCopy := &GraphNode{
			ID:       node.ID,
			Kind:     node.Kind,
			Content:  append([]byte(nil), node.Content...),
			State:    node.State,
			Metadata: make(map[string]interface{}),
		}

		for k, v := range node.Metadata {
			nodeCopy.Metadata[k] = v
		}

		result = append(result, nodeCopy)

		// 遍历子节点（根据方向）
		for _, edge := range s.edges {
			var nextNodeID string
			matched := false

			// 根据方向匹配边
			switch query.Direction {
			case GraphTraverseOut, "":
				if edge.From == nodeID {
					nextNodeID = edge.To
					matched = true
				}
			case GraphTraverseIn:
				if edge.To == nodeID {
					nextNodeID = edge.From
					matched = true
				}
			case GraphTraverseBoth:
				if edge.From == nodeID {
					nextNodeID = edge.To
					matched = true
				} else if edge.To == nodeID {
					nextNodeID = edge.From
					matched = true
				}
			}

			if !matched {
				continue
			}

			// 过滤边关系
			if len(query.Relations) > 0 {
				found := false
				for _, rel := range query.Relations {
					if edge.Relation == rel {
						found = true
						break
					}
				}
				if !found {
					continue
				}
			}

			traverse(nextNodeID, depth+1)
		}
	}

	traverse(startID, 0)
	return result, nil
}
