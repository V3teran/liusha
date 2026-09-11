package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// GraphImpl 是 Graph 的完整实现。
type GraphImpl struct {
	mu    sync.RWMutex
	nodes map[string]*Node
	edges []Edge

	// 依赖关系索引（优化查询）
	dependents map[string][]string // nodeID -> 依赖此节点的节点列表
	dependencies map[string][]string // nodeID -> 此节点依赖的节点列表
}

// NewGraph 创建新图。
func NewGraph() *GraphImpl {
	return &GraphImpl{
		nodes:        make(map[string]*Node),
		edges:        make([]Edge, 0),
		dependents:   make(map[string][]string),
		dependencies: make(map[string][]string),
	}
}

// AddNode 添加节点。
func (g *GraphImpl) AddNode(node Node) error {
	if node.ID == "" {
		return fmt.Errorf("node ID cannot be empty")
	}

	g.mu.Lock()
	defer g.mu.Unlock()

	// 检查是否已存在
	if _, exists := g.nodes[node.ID]; exists {
		return fmt.Errorf("node already exists: %s", node.ID)
	}

	// 初始化节点状态
	if node.State == "" {
		node.State = NodeStatePending
	}
	node.Metadata.CreatedAt = time.Now().UnixMilli()

	g.nodes[node.ID] = &node
	return nil
}

// AddEdge 添加边。
func (g *GraphImpl) AddEdge(from, to string, edgeType EdgeType) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	// 检查节点存在性
	if _, exists := g.nodes[from]; !exists {
		return ErrNodeNotFound{NodeID: from}
	}
	if _, exists := g.nodes[to]; !exists {
		return ErrNodeNotFound{NodeID: to}
	}

	// 添加边
	edge := Edge{
		From: from,
		To:   to,
		Type: edgeType,
	}
	g.edges = append(g.edges, edge)

	// 更新依赖索引
	g.dependents[from] = append(g.dependents[from], to)
	g.dependencies[to] = append(g.dependencies[to], from)

	// 更新节点的 DependsOn
	toNode := g.nodes[to]
	toNode.DependsOn = append(toNode.DependsOn, from)

	return nil
}

// GetNode 获取节点。
func (g *GraphImpl) GetNode(id string) (Node, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	node, exists := g.nodes[id]
	if !exists {
		return Node{}, ErrNodeNotFound{NodeID: id}
	}

	return *node, nil
}

// ListNodes 列出节点（支持过滤）。
func (g *GraphImpl) ListNodes(filter NodeFilter) ([]Node, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	result := make([]Node, 0)

	for _, node := range g.nodes {
		// 应用过滤器
		if filter.Type != "" && node.Type != filter.Type {
			continue
		}
		if filter.State != "" && node.State != filter.State {
			continue
		}
		if filter.ParallelGroup != "" && node.ParallelGroup != filter.ParallelGroup {
			continue
		}
		if len(filter.Labels) > 0 {
			match := true
			for k, v := range filter.Labels {
				if node.Metadata.Labels[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		result = append(result, *node)
	}

	return result, nil
}

// ListEdges 列出所有边。
func (g *GraphImpl) ListEdges() ([]Edge, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	// 返回副本
	edges := make([]Edge, len(g.edges))
	copy(edges, g.edges)
	return edges, nil
}

// NextNodes 获取下一批可执行的节点（依赖已满足）。
func (g *GraphImpl) NextNodes(ctx context.Context) ([]Node, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	ready := make([]Node, 0)

	for _, node := range g.nodes {
		// 只考虑 pending 状态的节点
		if node.State != NodeStatePending {
			continue
		}

		// 检查依赖是否满足
		if g.isDependencySatisfiedLocked(node.ID) {
			// 标记为 ready
			node.State = NodeStateReady
			ready = append(ready, *node)
		}
	}

	return ready, nil
}

// isDependencySatisfiedLocked 检查依赖是否满足（需要持有锁）。
func (g *GraphImpl) isDependencySatisfiedLocked(nodeID string) bool {
	deps := g.dependencies[nodeID]

	for _, depID := range deps {
		depNode := g.nodes[depID]
		if depNode.State != NodeStateCompleted {
			return false
		}
	}

	return true
}

// UpdateNodeState 更新节点状态。
func (g *GraphImpl) UpdateNodeState(ctx context.Context, nodeID string, state NodeState) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	node, exists := g.nodes[nodeID]
	if !exists {
		return ErrNodeNotFound{NodeID: nodeID}
	}

	oldState := node.State
	node.State = state

	// 更新时间戳
	now := time.Now().UnixMilli()
	switch state {
	case NodeStateRunning:
		node.Metadata.StartedAt = now
	case NodeStateCompleted, NodeStateFailed, NodeStateSkipped:
		node.Metadata.CompletedAt = now
		if node.Metadata.StartedAt > 0 {
			node.Metadata.DurationMs = now - node.Metadata.StartedAt
		}
	}

	// 状态转换验证
	if !g.isValidTransition(oldState, state) {
		return ErrInvalidState{From: string(oldState), To: string(state)}
	}

	return nil
}

// isValidTransition 验证状态转换是否合法。
func (g *GraphImpl) isValidTransition(from, to NodeState) bool {
	validTransitions := map[NodeState][]NodeState{
		NodeStatePending: {NodeStateReady, NodeStateSkipped},
		NodeStateReady:   {NodeStateRunning, NodeStateSkipped},
		NodeStateRunning: {NodeStateCompleted, NodeStateFailed, NodeStateBlocked},
		NodeStateCompleted: {},
		NodeStateFailed:    {},
		NodeStateSkipped:   {},
		NodeStateBlocked:   {NodeStateReady}, // 可以从阻塞恢复
	}

	allowed := validTransitions[from]
	for _, s := range allowed {
		if s == to {
			return true
		}
	}
	return false
}

// Clone 克隆图（用于子任务）。
func (g *GraphImpl) Clone() (Graph, error) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	cloned := NewGraph()

	// 克隆节点
	for _, node := range g.nodes {
		nodeCopy := *node
		cloned.nodes[node.ID] = &nodeCopy
	}

	// 克隆边
	cloned.edges = make([]Edge, len(g.edges))
	copy(cloned.edges, g.edges)

	// 克隆索引
	for k, v := range g.dependents {
		cloned.dependents[k] = make([]string, len(v))
		copy(cloned.dependents[k], v)
	}
	for k, v := range g.dependencies {
		cloned.dependencies[k] = make([]string, len(v))
		copy(cloned.dependencies[k], v)
	}

	return cloned, nil
}

// GetDependents 获取依赖此节点的节点列表。
func (g *GraphImpl) GetDependents(nodeID string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	deps := g.dependents[nodeID]
	result := make([]string, len(deps))
	copy(result, deps)
	return result
}

// GetDependencies 获取此节点依赖的节点列表。
func (g *GraphImpl) GetDependencies(nodeID string) []string {
	g.mu.RLock()
	defer g.mu.RUnlock()

	deps := g.dependencies[nodeID]
	result := make([]string, len(deps))
	copy(result, deps)
	return result
}

// Stats 获取图的统计信息。
func (g *GraphImpl) Stats() GraphStats {
	g.mu.RLock()
	defer g.mu.RUnlock()

	stats := GraphStats{
		TotalNodes: len(g.nodes),
		TotalEdges: len(g.edges),
		StateCount: make(map[NodeState]int),
	}

	for _, node := range g.nodes {
		stats.StateCount[node.State]++
	}

	return stats
}

// GraphStats 是图的统计信息。
type GraphStats struct {
	TotalNodes int                 `json:"total_nodes"`
	TotalEdges int                 `json:"total_edges"`
	StateCount map[NodeState]int   `json:"state_count"`
}
