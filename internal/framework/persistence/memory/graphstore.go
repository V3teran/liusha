// Package memory 提供框架核心接口的进程内实现，专供单元测试做快速替身——
// 生产装配一律走 postgres 版（见 explorationgraph.NewStore 与 runner 装配）。
package memory

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
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
	}

	// 排序后再截取，与 Postgres 的 ORDER BY ... LIMIT 语义一致（map 遍历序随机）
	sortGraphNodes(result, query.OrderBy)
	if query.Offset > 0 {
		if query.Offset >= len(result) {
			return []*core.GraphNode{}, nil
		}
		result = result[query.Offset:]
	}
	if query.Limit > 0 && len(result) > query.Limit {
		result = result[:query.Limit]
	}

	return result, nil
}

// sortGraphNodes 按 OrderBy 排序（支持 created_at / updated_at，"-" 前缀降序）；
// 同值按 ID 兜底，保证结果确定。未指定排序时按 ID 排，避免随机序。
func sortGraphNodes(nodes []*core.GraphNode, orderBy string) {
	desc := strings.HasPrefix(orderBy, "-")
	field := strings.TrimPrefix(orderBy, "-")
	key := func(n *core.GraphNode) time.Time {
		switch field {
		case "created_at":
			return n.CreatedAt
		case "updated_at":
			return n.UpdatedAt
		default:
			return time.Time{}
		}
	}
	sort.SliceStable(nodes, func(i, j int) bool {
		ki, kj := key(nodes[i]), key(nodes[j])
		if !ki.Equal(kj) {
			if desc {
				return ki.After(kj)
			}
			return ki.Before(kj)
		}
		return nodes[i].ID < nodes[j].ID
	})
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

// ApplyChangeSet 在单锁内先校验整个变更集、再统一生效——任一校验不过即原样
// 返回、不留部分写入，与 Postgres 事务实现的"全有或全无"语义一致：新节点先于
// 迁移、批注与边生效，后三者都可以作用于本批新节点。
func (s *GraphStore) ApplyChangeSet(_ context.Context, changes core.GraphChangeSet) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	newNodes, err := s.checkNewNodes(changes.Nodes)
	if err != nil {
		return err
	}
	lookup := func(id string) (*core.GraphNode, bool) {
		if n, ok := newNodes[id]; ok {
			return n, true
		}
		n, ok := s.nodes[id]
		return n, ok
	}
	if err := checkTransitions(changes.Transitions, lookup); err != nil {
		return err
	}
	if err := checkAnnotations(changes.Annotations, lookup); err != nil {
		return err
	}
	if err := checkEdges(changes.Edges, lookup); err != nil {
		return err
	}
	s.applyChangeSet(changes)
	return nil
}

// checkNewNodes 校验新节点 ID 非空且不与图中/本批节点重复（调用方持锁）。
func (s *GraphStore) checkNewNodes(nodes []*core.GraphNode) (map[string]*core.GraphNode, error) {
	newNodes := make(map[string]*core.GraphNode, len(nodes))
	for _, node := range nodes {
		if node == nil || node.ID == "" {
			return nil, fmt.Errorf("node ID cannot be empty")
		}
		if _, exists := s.nodes[node.ID]; exists {
			return nil, fmt.Errorf("node %s already exists", node.ID)
		}
		if _, dup := newNodes[node.ID]; dup {
			return nil, fmt.Errorf("node %s already exists", node.ID)
		}
		newNodes[node.ID] = node
	}
	return newNodes, nil
}

// checkTransitions 按序校验迁移前置：同一变更集内对同一节点的连续迁移以前一条
// 的结果为前置。
func checkTransitions(transitions []core.GraphStateTransition, lookup func(string) (*core.GraphNode, bool)) error {
	states := make(map[string]string, len(transitions))
	for _, t := range transitions {
		if t.TaskID == "" || t.NodeID == "" || t.From == "" || t.To == "" {
			return fmt.Errorf("state transition requires task_id, node_id, from and to")
		}
		node, exists := lookup(t.NodeID)
		current, seen := states[t.NodeID]
		if !seen && exists {
			current = node.State
		}
		if !exists || taskOf(node) != t.TaskID || current != t.From {
			return fmt.Errorf("transition %s %s→%s: %w", t.NodeID, t.From, t.To, core.ErrGraphStateConflict)
		}
		states[t.NodeID] = t.To
	}
	return nil
}

// checkAnnotations 校验批注目标存在且属于声明的任务；一次性批注（Once）的键
// 不得已在节点上，也不得已由本批更早的批注写入（与 Postgres 逐条 UPDATE 同序）。
func checkAnnotations(annotations []core.GraphNodeAnnotation, lookup func(string) (*core.GraphNode, bool)) error {
	written := map[string]map[string]bool{} // 本批已写入的键：nodeID → keys
	for _, a := range annotations {
		if a.TaskID == "" || a.NodeID == "" || len(a.Metadata) == 0 {
			return fmt.Errorf("annotation requires task_id, node_id and metadata")
		}
		node, exists := lookup(a.NodeID)
		if !exists || taskOf(node) != a.TaskID {
			return fmt.Errorf("annotate %s: %w", a.NodeID, core.ErrGraphNodeNotFound)
		}
		if written[a.NodeID] == nil {
			written[a.NodeID] = map[string]bool{}
		}
		for k := range a.Metadata {
			_, onNode := node.Metadata[k]
			if a.Once && (onNode || written[a.NodeID][k]) {
				return fmt.Errorf("annotate %s: key %q already set: %w", a.NodeID, k, core.ErrGraphStateConflict)
			}
		}
		for k := range a.Metadata {
			written[a.NodeID][k] = true
		}
	}
	return nil
}

// checkEdges 校验边的任务归属与端点存在（端点可以是本批新节点）。
func checkEdges(edges []*core.GraphEdge, lookup func(string) (*core.GraphNode, bool)) error {
	for _, edge := range edges {
		if edge == nil || edge.TaskID == "" {
			return fmt.Errorf("edge task_id cannot be empty")
		}
		for _, end := range []string{edge.From, edge.To} {
			if _, exists := lookup(end); !exists {
				return fmt.Errorf("%w: edge endpoint %s", core.ErrGraphNodeNotFound, end)
			}
		}
	}
	return nil
}

// applyChangeSet 让已校验的变更集生效：建节点 → 迁移 → 批注 → 建边（调用方持锁）。
func (s *GraphStore) applyChangeSet(changes core.GraphChangeSet) {
	now := time.Now()
	for _, node := range changes.Nodes {
		nodeCopy := copyGraphNode(node)
		if nodeCopy.Version == 0 {
			nodeCopy.Version = 1
		}
		if nodeCopy.CreatedAt.IsZero() {
			nodeCopy.CreatedAt = now
		}
		if nodeCopy.UpdatedAt.IsZero() {
			nodeCopy.UpdatedAt = now
		}
		s.nodes[node.ID] = nodeCopy
	}
	// 逐条迁移（与 Postgres 每条 UPDATE 各自触发版本递增一致）
	for _, t := range changes.Transitions {
		node := s.nodes[t.NodeID]
		node.State = t.To
		node.Version++
		node.UpdatedAt = now
	}
	for _, a := range changes.Annotations {
		node := s.nodes[a.NodeID]
		for k, v := range a.Metadata {
			node.Metadata[k] = v
		}
		node.Version++
		node.UpdatedAt = now
	}
	for _, edge := range changes.Edges {
		if s.hasEdge(edge) {
			continue
		}
		edgeCopy := &core.GraphEdge{
			TaskID:    edge.TaskID,
			From:      edge.From,
			To:        edge.To,
			Relation:  edge.Relation,
			Metadata:  make(map[string]interface{}, len(edge.Metadata)),
			CreatedAt: edge.CreatedAt,
		}
		for k, v := range edge.Metadata {
			edgeCopy.Metadata[k] = v
		}
		s.edges = append(s.edges, edgeCopy)
	}
}

// taskOf 读取节点的任务归属（metadata.task_id）。
func taskOf(node *core.GraphNode) string {
	tid, _ := node.Metadata["task_id"].(string)
	return tid
}

// hasEdge 判断同 (task_id, from, relation, to) 的边是否已存在（调用方持锁）。
func (s *GraphStore) hasEdge(edge *core.GraphEdge) bool {
	for _, existing := range s.edges {
		if existing.TaskID == edge.TaskID && existing.From == edge.From &&
			existing.Relation == edge.Relation && existing.To == edge.To {
			return true
		}
	}
	return false
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
