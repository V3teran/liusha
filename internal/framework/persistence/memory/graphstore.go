// Package memory 提供框架核心接口的进程内实现，专供单元测试做快速替身——
// 生产装配一律走 postgres 版（见 explorationgraph.NewStore 与 runner 装配）。
package memory

import (
	"context"
	"fmt"
	"github.com/V3teran/liusha/internal/framework/core"
	"sync"
	"time"
)

// GraphStore 内存实现的 core.GraphStore（用于测试）
type GraphStore struct {
	mu    sync.RWMutex
	nodes map[string]*core.GraphNode
	edges []*core.GraphEdge
}

// NewGraphStore 创建内存 core.GraphStore
func NewGraphStore() core.GraphStore {
	return &GraphStore{
		nodes: make(map[string]*core.GraphNode),
		edges: make([]*core.GraphEdge, 0),
	}
}

// copyGraphNode 深拷贝节点（含全部字段），避免调用方与 store 内部状态共享引用。
func copyGraphNode(node *core.GraphNode) *core.GraphNode {
	nodeCopy := &core.GraphNode{
		ID:        node.ID,
		Kind:      node.Kind,
		Content:   append([]byte(nil), node.Content...),
		State:     node.State,
		Version:   node.Version,
		Metadata:  make(map[string]interface{}),
		CreatedAt: node.CreatedAt,
		UpdatedAt: node.UpdatedAt,
	}

	for k, v := range node.Metadata {
		nodeCopy.Metadata[k] = v
	}
	return nodeCopy
}

// CreateNode 创建节点
func (s *GraphStore) CreateNode(_ context.Context, node *core.GraphNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.nodes[node.ID]; exists {
		return fmt.Errorf("node %s already exists", node.ID)
	}

	nodeCopy := copyGraphNode(node)

	// Version 默认为 1
	if nodeCopy.Version == 0 {
		nodeCopy.Version = 1
	}

	// 时间戳由 store 维护：未显式指定时取当前时间
	now := time.Now()
	if nodeCopy.CreatedAt.IsZero() {
		nodeCopy.CreatedAt = now
	}
	if nodeCopy.UpdatedAt.IsZero() {
		nodeCopy.UpdatedAt = now
	}

	for k, v := range node.Metadata {
		nodeCopy.Metadata[k] = v
	}

	s.nodes[node.ID] = nodeCopy
	return nil
}

// GetNode 获取节点
func (s *GraphStore) GetNode(_ context.Context, id string) (*core.GraphNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node, exists := s.nodes[id]
	if !exists {
		return nil, core.ErrGraphNodeNotFound
	}

	return copyGraphNode(node), nil
}

// UpdateNode 更新节点
func (s *GraphStore) UpdateNode(_ context.Context, id string, update core.GraphNodeUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[id]
	if !exists {
		return core.ErrGraphNodeNotFound
	}

	// 乐观锁检查
	if update.ExpectedVersion != nil {
		if node.Version != *update.ExpectedVersion {
			return core.ErrVersionMismatch
		}
	}

	if update.Content != nil {
		node.Content = append([]byte(nil), update.Content...)
	}

	if update.State != "" {
		node.State = update.State
	}

	if update.Metadata != nil {
		for k, v := range update.Metadata {
			node.Metadata[k] = v
		}
	}

	// 更新后递增 version，并刷新 UpdatedAt
	node.Version++
	node.UpdatedAt = time.Now()

	return nil
}

// CompareAndSwapState 原子更新节点状态（单锁内完成条件检查与写入）。
func (s *GraphStore) CompareAndSwapState(_ context.Context, taskID, nodeID string, expectedState, newState string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[nodeID]
	if !exists {
		return false, core.ErrGraphNodeNotFound
	}

	// 检查 taskID（如果提供）
	if taskID != "" {
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
func (s *GraphStore) DeleteNode(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.nodes[id]; !exists {
		return fmt.Errorf("%w: %s", core.ErrGraphNodeNotFound, id)
	}

	delete(s.nodes, id)

	// 删除相关边
	newEdges := make([]*core.GraphEdge, 0)
	for _, edge := range s.edges {
		if edge.From != id && edge.To != id {
			newEdges = append(newEdges, edge)
		}
	}
	s.edges = newEdges

	return nil
}

// ListNodes 查询节点列表
func (s *GraphStore) ListNodes(_ context.Context, query core.GraphNodeQuery) ([]*core.GraphNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*core.GraphNode, 0)

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
			} else if node.Metadata[k] != v {
				// 直接访问
				match = false
				break
			}
		}

		if !match {
			continue
		}

		result = append(result, copyGraphNode(node))

		// 限制数量
		if query.Limit > 0 && len(result) >= query.Limit {
			break
		}
	}

	return result, nil
}

// CreateNodeWithEdges 创建节点及边，失败时回滚本次已写入的部分——
// 与 Postgres 事务实现保持一致的"全有或全无"语义。
func (s *GraphStore) CreateNodeWithEdges(ctx context.Context, node *core.GraphNode, edges []*core.GraphEdge) error {
	if err := s.CreateNode(ctx, node); err != nil {
		return err
	}
	created := make([]*core.GraphEdge, 0, len(edges))
	for _, edge := range edges {
		if err := s.CreateEdge(ctx, edge); err != nil {
			// 回滚：删除已建的边与节点
			for _, e := range created {
				_ = s.DeleteEdge(ctx, e.From, e.To, e.Relation)
			}
			_ = s.DeleteNode(ctx, node.ID)
			return err
		}
		created = append(created, edge)
	}
	return nil
}

// CreateEdge 创建边
func (s *GraphStore) CreateEdge(_ context.Context, edge *core.GraphEdge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if edge.TaskID == "" {
		return fmt.Errorf("edge task_id cannot be empty")
	}

	// 检查节点是否存在
	if _, exists := s.nodes[edge.From]; !exists {
		return fmt.Errorf("%w: from node %s", core.ErrGraphNodeNotFound, edge.From)
	}

	if _, exists := s.nodes[edge.To]; !exists {
		return fmt.Errorf("%w: to node %s", core.ErrGraphNodeNotFound, edge.To)
	}

	// 深拷贝
	edgeCopy := &core.GraphEdge{
		TaskID:   edge.TaskID,
		From:     edge.From,
		To:       edge.To,
		Relation: edge.Relation,
		Metadata: make(map[string]interface{}),
	}

	for k, v := range edge.Metadata {
		edgeCopy.Metadata[k] = v
	}

	// 幂等：同 (task_id, from, relation, to) 已存在则跳过
	for _, existing := range s.edges {
		if existing.TaskID == edge.TaskID && existing.From == edge.From &&
			existing.Relation == edge.Relation && existing.To == edge.To {
			return nil
		}
	}

	s.edges = append(s.edges, edgeCopy)
	return nil
}

// ListEdges 查询边列表
func (s *GraphStore) ListEdges(_ context.Context, query core.GraphEdgeQuery) ([]*core.GraphEdge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*core.GraphEdge, 0)

	for _, edge := range s.edges {
		// 过滤 TaskID
		if query.TaskID != "" && edge.TaskID != query.TaskID {
			continue
		}

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
		edgeCopy := &core.GraphEdge{
			TaskID:   edge.TaskID,
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
func (s *GraphStore) DeleteEdge(_ context.Context, from, to, relation string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	found := false
	newEdges := make([]*core.GraphEdge, 0)

	for _, edge := range s.edges {
		if edge.From == from && edge.To == to && edge.Relation == relation {
			found = true
		} else {
			newEdges = append(newEdges, edge)
		}
	}

	if !found {
		return fmt.Errorf("%w", core.ErrGraphEdgeNotFound)
	}

	s.edges = newEdges
	return nil
}
