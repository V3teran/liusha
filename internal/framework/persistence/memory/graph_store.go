package memory

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/framework/persistence"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// GraphStore 是内存实现的知识图谱存储
type GraphStore struct {
	nodes         map[string]*knowledgegraph.Node // nodeID -> Node
	edges         map[string]*knowledgegraph.Edge // edgeKey -> Edge
	verifications map[string]*knowledgegraph.Verification // verificationID -> Verification

	// 索引：加速查询
	nodesByTask   map[string]map[string]*knowledgegraph.Node // taskID -> nodeID -> Node
	edgesByTask   map[string]map[string]*knowledgegraph.Edge // taskID -> edgeKey -> Edge
	verifyByTask  map[string]map[string]*knowledgegraph.Verification // taskID -> verificationID -> Verification

	mu sync.RWMutex
}

// NewGraphStore 创建内存图存储
func NewGraphStore() *GraphStore {
	return &GraphStore{
		nodes:         make(map[string]*knowledgegraph.Node),
		edges:         make(map[string]*knowledgegraph.Edge),
		verifications: make(map[string]*knowledgegraph.Verification),
		nodesByTask:   make(map[string]map[string]*knowledgegraph.Node),
		edgesByTask:   make(map[string]map[string]*knowledgegraph.Edge),
		verifyByTask:  make(map[string]map[string]*knowledgegraph.Verification),
	}
}

// ========== Node 操作 ==========

func (s *GraphStore) CreateNode(ctx context.Context, node knowledgegraph.Node) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if node.ID == "" {
		return "", fmt.Errorf("node ID is required")
	}
	if node.TaskID == "" {
		return "", fmt.Errorf("task ID is required")
	}

	// 检查是否已存在
	if _, exists := s.nodes[node.ID]; exists {
		return "", fmt.Errorf("node %s already exists", node.ID)
	}

	// 设置时间戳
	if node.CreatedAt.IsZero() {
		node.CreatedAt = time.Now()
	}
	if node.UpdatedAt.IsZero() {
		node.UpdatedAt = time.Now()
	}

	// 存储节点
	nodeCopy := node
	s.nodes[node.ID] = &nodeCopy

	// 更新索引
	if s.nodesByTask[node.TaskID] == nil {
		s.nodesByTask[node.TaskID] = make(map[string]*knowledgegraph.Node)
	}
	s.nodesByTask[node.TaskID][node.ID] = &nodeCopy

	return node.ID, nil
}

func (s *GraphStore) GetNode(ctx context.Context, taskID, nodeID string) (*knowledgegraph.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	node, exists := s.nodes[nodeID]
	if !exists {
		return nil, fmt.Errorf("node %s not found", nodeID)
	}
	if node.TaskID != taskID {
		return nil, fmt.Errorf("node %s does not belong to task %s", nodeID, taskID)
	}

	nodeCopy := *node
	return &nodeCopy, nil
}

func (s *GraphStore) UpdateNode(ctx context.Context, node knowledgegraph.Node) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, exists := s.nodes[node.ID]
	if !exists {
		return fmt.Errorf("node %s not found", node.ID)
	}
	if existing.TaskID != node.TaskID {
		return fmt.Errorf("cannot change task ID")
	}

	// 更新时间戳
	node.UpdatedAt = time.Now()
	node.CreatedAt = existing.CreatedAt // 保留创建时间

	// 更新节点
	nodeCopy := node
	s.nodes[node.ID] = &nodeCopy
	s.nodesByTask[node.TaskID][node.ID] = &nodeCopy

	return nil
}

// CompareAndSwapState 原子性地比较并交换节点状态
func (s *GraphStore) CompareAndSwapState(ctx context.Context, taskID, nodeID string, expectedState, newState string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[nodeID]
	if !exists {
		return false, fmt.Errorf("node %s not found", nodeID)
	}
	if node.TaskID != taskID {
		return false, fmt.Errorf("node %s does not belong to task %s", nodeID, taskID)
	}

	// 检查当前状态
	currentState := ""
	if node.State != nil {
		currentState = string(*node.State)
	}

	if currentState != expectedState {
		return false, nil // 状态不匹配，CAS 失败
	}

	// 更新状态
	newStateTyped := knowledgegraph.State(newState)
	node.State = &newStateTyped
	node.UpdatedAt = time.Now()

	return true, nil
}

func (s *GraphStore) DeleteNode(ctx context.Context, taskID, nodeID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	node, exists := s.nodes[nodeID]
	if !exists {
		return fmt.Errorf("node %s not found", nodeID)
	}
	if node.TaskID != taskID {
		return fmt.Errorf("node %s does not belong to task %s", nodeID, taskID)
	}

	delete(s.nodes, nodeID)
	delete(s.nodesByTask[taskID], nodeID)

	return nil
}

func (s *GraphStore) ListNodes(ctx context.Context, filter persistence.NodeFilter) ([]knowledgegraph.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if filter.TaskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}

	taskNodes, exists := s.nodesByTask[filter.TaskID]
	if !exists {
		return []knowledgegraph.Node{}, nil
	}

	// 过滤节点
	var result []knowledgegraph.Node
	for _, node := range taskNodes {
		if s.matchNodeFilter(node, filter) {
			result = append(result, *node)
		}
	}

	// 分页
	if filter.Offset > 0 {
		if filter.Offset >= len(result) {
			return []knowledgegraph.Node{}, nil
		}
		result = result[filter.Offset:]
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}

	return result, nil
}

// matchNodeFilter 检查节点是否匹配过滤器
func (s *GraphStore) matchNodeFilter(node *knowledgegraph.Node, filter persistence.NodeFilter) bool {
	if filter.Kind != nil && string(node.Kind) != *filter.Kind {
		return false
	}
	if filter.State != nil && (node.State == nil || *node.State != *filter.State) {
		return false
	}
	if filter.Confidence != nil && (node.Confidence == nil || *node.Confidence != *filter.Confidence) {
		return false
	}
	if filter.Priority != nil && node.Priority != *filter.Priority {
		return false
	}
	if filter.Owner != nil && node.Owner != *filter.Owner {
		return false
	}
	if filter.SourceType != nil && node.SourceType != *filter.SourceType {
		return false
	}
	if len(filter.Tags) > 0 {
		tagSet := make(map[string]bool)
		for _, tag := range node.Tags {
			tagSet[tag] = true
		}
		for _, filterTag := range filter.Tags {
			if !tagSet[filterTag] {
				return false
			}
		}
	}
	return true
}

// ========== Edge 操作 ==========

func (s *GraphStore) CreateEdge(ctx context.Context, edge knowledgegraph.Edge) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if edge.TaskID == "" || edge.SrcID == "" || edge.DstID == "" {
		return fmt.Errorf("task ID, src ID, and dst ID are required")
	}

	key := s.edgeKey(edge.TaskID, edge.SrcID, edge.Rel, edge.DstID)
	if _, exists := s.edges[key]; exists {
		return fmt.Errorf("edge already exists")
	}

	if edge.CreatedAt.IsZero() {
		edge.CreatedAt = time.Now()
	}

	edgeCopy := edge
	s.edges[key] = &edgeCopy

	if s.edgesByTask[edge.TaskID] == nil {
		s.edgesByTask[edge.TaskID] = make(map[string]*knowledgegraph.Edge)
	}
	s.edgesByTask[edge.TaskID][key] = &edgeCopy

	return nil
}

func (s *GraphStore) GetEdge(ctx context.Context, taskID, srcID string, rel knowledgegraph.Relation, dstID string) (*knowledgegraph.Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	key := s.edgeKey(taskID, srcID, rel, dstID)
	edge, exists := s.edges[key]
	if !exists {
		return nil, fmt.Errorf("edge not found")
	}

	edgeCopy := *edge
	return &edgeCopy, nil
}

func (s *GraphStore) DeleteEdge(ctx context.Context, taskID, srcID string, rel knowledgegraph.Relation, dstID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := s.edgeKey(taskID, srcID, rel, dstID)
	if _, exists := s.edges[key]; !exists {
		return fmt.Errorf("edge not found")
	}

	delete(s.edges, key)
	delete(s.edgesByTask[taskID], key)

	return nil
}

func (s *GraphStore) ListEdges(ctx context.Context, filter persistence.EdgeFilter) ([]knowledgegraph.Edge, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if filter.TaskID == "" {
		return nil, fmt.Errorf("task ID is required")
	}

	taskEdges, exists := s.edgesByTask[filter.TaskID]
	if !exists {
		return []knowledgegraph.Edge{}, nil
	}

	var result []knowledgegraph.Edge
	for _, edge := range taskEdges {
		if s.matchEdgeFilter(edge, filter) {
			result = append(result, *edge)
		}
	}

	// 分页
	if filter.Offset > 0 {
		if filter.Offset >= len(result) {
			return []knowledgegraph.Edge{}, nil
		}
		result = result[filter.Offset:]
	}
	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}

	return result, nil
}

func (s *GraphStore) matchEdgeFilter(edge *knowledgegraph.Edge, filter persistence.EdgeFilter) bool {
	if filter.SrcID != nil && edge.SrcID != *filter.SrcID {
		return false
	}
	if filter.DstID != nil && edge.DstID != *filter.DstID {
		return false
	}
	if filter.Rel != nil && edge.Rel != *filter.Rel {
		return false
	}
	return true
}

func (s *GraphStore) edgeKey(taskID, srcID string, rel knowledgegraph.Relation, dstID string) string {
	return fmt.Sprintf("%s:%s:%s:%s", taskID, srcID, rel, dstID)
}

// ========== Verification 操作 ==========

func (s *GraphStore) RecordVerification(ctx context.Context, v knowledgegraph.Verification) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if v.ID == "" || v.TaskID == "" {
		return "", fmt.Errorf("verification ID and task ID are required")
	}

	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now()
	}

	vCopy := v
	s.verifications[v.ID] = &vCopy

	if s.verifyByTask[v.TaskID] == nil {
		s.verifyByTask[v.TaskID] = make(map[string]*knowledgegraph.Verification)
	}
	s.verifyByTask[v.TaskID][v.ID] = &vCopy

	return v.ID, nil
}

func (s *GraphStore) GetVerification(ctx context.Context, taskID, verificationID string) (*knowledgegraph.Verification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	v, exists := s.verifications[verificationID]
	if !exists {
		return nil, fmt.Errorf("verification %s not found", verificationID)
	}
	if v.TaskID != taskID {
		return nil, fmt.Errorf("verification %s does not belong to task %s", verificationID, taskID)
	}

	vCopy := *v
	return &vCopy, nil
}

func (s *GraphStore) ListVerifications(ctx context.Context, taskID string) ([]knowledgegraph.Verification, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	taskVerifications, exists := s.verifyByTask[taskID]
	if !exists {
		return []knowledgegraph.Verification{}, nil
	}

	result := make([]knowledgegraph.Verification, 0, len(taskVerifications))
	for _, v := range taskVerifications {
		result = append(result, *v)
	}

	return result, nil
}

// ========== 图查询 ==========

func (s *GraphStore) GetSubgraph(ctx context.Context, taskID, startNodeID string, maxDepth int) (*persistence.Subgraph, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// BFS 遍历
	visited := make(map[string]bool)
	queue := []struct {
		nodeID string
		depth  int
	}{{startNodeID, 0}}

	var nodes []knowledgegraph.Node
	var edges []knowledgegraph.Edge

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if visited[current.nodeID] || current.depth > maxDepth {
			continue
		}
		visited[current.nodeID] = true

		// 添加节点
		if node, exists := s.nodes[current.nodeID]; exists && node.TaskID == taskID {
			nodes = append(nodes, *node)

			// 查找所有出边
			if taskEdges, exists := s.edgesByTask[taskID]; exists {
				for _, edge := range taskEdges {
					if edge.SrcID == current.nodeID {
						edges = append(edges, *edge)
						queue = append(queue, struct {
							nodeID string
							depth  int
						}{edge.DstID, current.depth + 1})
					}
				}
			}
		}
	}

	return &persistence.Subgraph{
		Nodes: nodes,
		Edges: edges,
	}, nil
}

func (s *GraphStore) GetActionsByState(ctx context.Context, taskID string, state knowledgegraph.State) ([]knowledgegraph.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	taskNodes, exists := s.nodesByTask[taskID]
	if !exists {
		return []knowledgegraph.Node{}, nil
	}

	var result []knowledgegraph.Node
	for _, node := range taskNodes {
		if node.IsAction() && node.State != nil && *node.State == state {
			result = append(result, *node)
		}
	}

	return result, nil
}

func (s *GraphStore) GetDependencyChain(ctx context.Context, taskID, actionID string) ([]knowledgegraph.Node, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	action, exists := s.nodes[actionID]
	if !exists || action.TaskID != taskID || !action.IsAction() {
		return nil, fmt.Errorf("action %s not found", actionID)
	}

	// 递归获取所有依赖
	visited := make(map[string]bool)
	var result []knowledgegraph.Node

	var collectDeps func(nodeID string)
	collectDeps = func(nodeID string) {
		if visited[nodeID] {
			return
		}
		visited[nodeID] = true

		if node, exists := s.nodes[nodeID]; exists && node.TaskID == taskID {
			result = append(result, *node)
			if node.IsAction() && len(node.DependsOn) > 0 {
				for _, depID := range node.DependsOn {
					collectDeps(depID)
				}
			}
		}
	}

	for _, depID := range action.DependsOn {
		collectDeps(depID)
	}

	return result, nil
}
