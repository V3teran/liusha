package core

import (
	"encoding/json"
	"fmt"
	"sync"
)

// Subgraph 是子图管理器。
type Subgraph struct {
	id           string
	parentGraph  *GraphImpl
	graph        *GraphImpl
	isolated     bool
	sharedState  map[string]any
	mu           sync.RWMutex
}

// NewSubgraph 创建子图。
func NewSubgraph(id string, parentGraph *GraphImpl, isolated bool) *Subgraph {
	return &Subgraph{
		id:          id,
		parentGraph: parentGraph,
		graph:       NewGraph(),
		isolated:    isolated,
		sharedState: make(map[string]any),
	}
}

// ID 返回子图 ID。
func (s *Subgraph) ID() string {
	return s.id
}

// Graph 返回子图的图实例。
func (s *Subgraph) Graph() *GraphImpl {
	return s.graph
}

// IsIsolated 判断是否隔离。
func (s *Subgraph) IsIsolated() bool {
	return s.isolated
}

// Merge 合并子图到父图。
func (s *Subgraph) Merge() error {
	if s.parentGraph == nil {
		return fmt.Errorf("no parent graph to merge into")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// 合并节点
	s.graph.mu.RLock()
	nodes := make([]*Node, 0, len(s.graph.nodes))
	for _, node := range s.graph.nodes {
		nodeCopy := *node
		nodes = append(nodes, &nodeCopy)
	}
	s.graph.mu.RUnlock()

	for _, node := range nodes {
		if err := s.parentGraph.AddNode(*node); err != nil {
			return fmt.Errorf("merge node %s: %w", node.ID, err)
		}
	}

	// 合并边
	s.graph.mu.RLock()
	edges := make([]Edge, len(s.graph.edges))
	copy(edges, s.graph.edges)
	s.graph.mu.RUnlock()

	for _, edge := range edges {
		if err := s.parentGraph.AddEdge(edge.From, edge.To, edge.Type); err != nil {
			return fmt.Errorf("merge edge %s->%s: %w", edge.From, edge.To, err)
		}
	}

	return nil
}

// Share 共享状态到父图。
func (s *Subgraph) Share(key string, value any) error {
	if s.isolated {
		return fmt.Errorf("cannot share state from isolated subgraph")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.sharedState[key] = value
	return nil
}

// GetShared 从父图获取共享状态。
func (s *Subgraph) GetShared(key string) (any, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	value, exists := s.sharedState[key]
	return value, exists
}

// Export 导出子图结果。
func (s *Subgraph) Export() (SubgraphExport, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	export := SubgraphExport{
		ID:          s.id,
		Isolated:    s.isolated,
		SharedState: make(map[string]json.RawMessage),
		Stats:       s.graph.Stats(),
	}

	// 序列化共享状态
	for key, value := range s.sharedState {
		data, err := json.Marshal(value)
		if err != nil {
			return export, fmt.Errorf("marshal shared state %s: %w", key, err)
		}
		export.SharedState[key] = data
	}

	return export, nil
}

// Clone 克隆子图。
func (s *Subgraph) Clone() (*Subgraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	clonedGraph, err := s.graph.Clone()
	if err != nil {
		return nil, fmt.Errorf("clone graph: %w", err)
	}

	cloned := &Subgraph{
		id:          s.id + "-clone",
		parentGraph: s.parentGraph,
		graph:       clonedGraph.(*GraphImpl),
		isolated:    s.isolated,
		sharedState: make(map[string]any),
	}

	// 复制共享状态
	for k, v := range s.sharedState {
		cloned.sharedState[k] = v
	}

	return cloned, nil
}

// SubgraphExport 是子图导出结果。
type SubgraphExport struct {
	ID          string                     `json:"id"`
	Isolated    bool                       `json:"isolated"`
	SharedState map[string]json.RawMessage `json:"shared_state"`
	Stats       GraphStats                 `json:"stats"`
}

// SubgraphManager 管理多个子图。
type SubgraphManager struct {
	mu        sync.RWMutex
	subgraphs map[string]*Subgraph
}

// NewSubgraphManager 创建子图管理器。
func NewSubgraphManager() *SubgraphManager {
	return &SubgraphManager{
		subgraphs: make(map[string]*Subgraph),
	}
}

// Create 创建子图。
func (m *SubgraphManager) Create(id string, parentGraph *GraphImpl, isolated bool) (*Subgraph, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.subgraphs[id]; exists {
		return nil, fmt.Errorf("subgraph already exists: %s", id)
	}

	subgraph := NewSubgraph(id, parentGraph, isolated)
	m.subgraphs[id] = subgraph
	return subgraph, nil
}

// Get 获取子图。
func (m *SubgraphManager) Get(id string) (*Subgraph, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	subgraph, exists := m.subgraphs[id]
	if !exists {
		return nil, fmt.Errorf("subgraph not found: %s", id)
	}

	return subgraph, nil
}

// Delete 删除子图。
func (m *SubgraphManager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.subgraphs[id]; !exists {
		return fmt.Errorf("subgraph not found: %s", id)
	}

	delete(m.subgraphs, id)
	return nil
}

// List 列出所有子图。
func (m *SubgraphManager) List() []*Subgraph {
	m.mu.RLock()
	defer m.mu.RUnlock()

	subgraphs := make([]*Subgraph, 0, len(m.subgraphs))
	for _, sg := range m.subgraphs {
		subgraphs = append(subgraphs, sg)
	}
	return subgraphs
}

// MergeAll 合并所有子图。
func (m *SubgraphManager) MergeAll() error {
	m.mu.RLock()
	subgraphs := make([]*Subgraph, 0, len(m.subgraphs))
	for _, sg := range m.subgraphs {
		subgraphs = append(subgraphs, sg)
	}
	m.mu.RUnlock()

	for _, sg := range subgraphs {
		if err := sg.Merge(); err != nil {
			return fmt.Errorf("merge subgraph %s: %w", sg.id, err)
		}
	}

	return nil
}

// Clear 清空所有子图。
func (m *SubgraphManager) Clear() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.subgraphs = make(map[string]*Subgraph)
}
