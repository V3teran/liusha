package explorationgraph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store 是探索图的 Framework 适配器：领域 Node/Edge 与通用 GraphNode/GraphEdge
// 的双向映射。pool 非 nil 时（Postgres 后端），统计/状态投影等查询直接 SQL 下推；
// 内存后端 pool 为 nil，退化为经 GraphStore 通用查询。
type Store struct {
	graphStore core.GraphStore
	pool       *pgxpool.Pool
}

// newStore 用 Framework GraphStore 组装 Store。公共入口是 factory.go 的
// NewStore(pool)/NewMemoryStore()——本构造器仅包内组装用。
func newStore(graphStore core.GraphStore) *Store {
	return &Store{
		graphStore: graphStore,
	}
}

// typedMetadataKeys 是 graph metadata 中映射到 Node 类型化字段的键。
// 其余键在读取时原样进入 Node.Metadata（业务扩展位：verification_outcome、
// failure_reason 等），写入时 Node.Metadata 的键平铺合并进 graph metadata。
var typedMetadataKeys = map[string]bool{
	"task_id":        true,
	"source_type":    true,
	"source_id":      true,
	"priority":       true,
	"complexity":     true,
	"depends_on":     true,
	"blocked_reason": true,
	"confidence":     true,
}

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

// CreateBusinessEdge 创建边。
func (s *Store) CreateBusinessEdge(ctx context.Context, edge Edge) error {
	return s.graphStore.CreateEdge(ctx, s.edgeToGraphEdge(&edge))
}

// nodeToGraphNode 领域节点 → 通用图节点。
func (s *Store) nodeToGraphNode(node Node) *core.GraphNode {
	metadata := map[string]interface{}{
		"task_id": node.TaskID,
	}
	// 空值不写入 metadata：让 GraphStore 落回默认值（source_type=system 等）。
	if node.SourceType != "" {
		metadata["source_type"] = string(node.SourceType)
	}
	if node.SourceID != "" {
		metadata["source_id"] = node.SourceID
	}
	if node.Priority != "" {
		metadata["priority"] = node.Priority
	}

	graphNode := &core.GraphNode{
		ID:        node.ID,
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
	if len(node.DependsOn) > 0 {
		graphNode.Metadata["depends_on"] = node.DependsOn
	}
	if node.BlockedReason != nil {
		graphNode.Metadata["blocked_reason"] = *node.BlockedReason
	}

	// Confidence：验证生命周期裁决（unverified/verified/refuted），枚举原文入库。
	// executor 的自评置信度（low/medium/high）属于假设载荷，走 Content，不进此处。
	if node.Confidence != nil {
		graphNode.Metadata["confidence"] = string(*node.Confidence)
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

	if len(edge.Attrs) > 0 {
		var attrs map[string]interface{}
		if err := json.Unmarshal(edge.Attrs, &attrs); err == nil {
			graphEdge.Metadata = attrs
		}
	}

	return graphEdge
}

// UpdateNodeContent 更新节点内容（控制平面 adjust_goal 等操作使用）。
func (s *Store) UpdateNodeContent(ctx context.Context, id string, content json.RawMessage) error {
	return s.graphStore.UpdateNode(ctx, id, core.GraphNodeUpdate{Content: content})
}

// UpdateActionStateWithReason 更新 action 状态和阻塞原因（单条 UPDATE，原子生效）。
// aborted 是终态，不允许覆盖（除非目标状态也是 aborted）。
func (s *Store) UpdateActionStateWithReason(ctx context.Context, id string, state State, blockedReason *string) error {
	// 先检查当前状态（防止覆盖 aborted）
	current, err := s.GetNode(ctx, id)
	if err != nil {
		return fmt.Errorf("get current state before update: %w", err)
	}

	// aborted 是终态，不允许覆盖（除非目标状态也是 aborted）
	if current.State != nil && *current.State == StateAborted && state != StateAborted {
		return fmt.Errorf("cannot overwrite aborted state (current: aborted, target: %s)", state)
	}

	update := core.GraphNodeUpdate{
		State: string(state),
	}

	if blockedReason != nil {
		update.Metadata = map[string]interface{}{
			"blocked_reason": *blockedReason,
		}
	}

	return s.graphStore.UpdateNode(ctx, id, update)
}

// CompareAndSwapActionState 原子性地比较并交换 action 状态。
// 只有当前状态等于 expectedState 时才更新为 newState，用于执行方抢占认领。
// 返回 (true, nil) 表示更新成功；(false, nil) 表示状态不匹配（已被抢占/已流转）。
func (s *Store) CompareAndSwapActionState(ctx context.Context, taskID, actionID string, expectedState, newState State) (bool, error) {
	return s.graphStore.CompareAndSwapState(ctx, taskID, actionID, string(expectedState), string(newState))
}

// ─────────────────────────────────────────────
// 查询
// ─────────────────────────────────────────────

// GetNode 获取节点。
func (s *Store) GetNode(ctx context.Context, id string) (*Node, error) {
	graphNode, err := s.graphStore.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.graphNodeToNode(graphNode), nil
}

// ListNodesByKind 列出指定 task 和 kind 的节点
func (s *Store) ListNodesByKind(ctx context.Context, taskID string, kind core.NodeKind) ([]Node, error) {
	query := core.GraphNodeQuery{
		Kind: string(kind),
		Filters: map[string]interface{}{
			"task_id": taskID,
		},
		OrderBy: "created_at",
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, err
	}

	return s.convertGraphNodesToNodes(nodes), nil
}

// ListActionsByState 列出指定 task 和 state 的 action 节点
func (s *Store) ListActionsByState(ctx context.Context, taskID string, state State) ([]Node, error) {
	query := core.GraphNodeQuery{
		Kind:  string(core.KindAction),
		State: string(state),
		Filters: map[string]interface{}{
			"task_id": taskID,
		},
		OrderBy: "created_at",
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, err
	}

	return s.convertGraphNodesToNodes(nodes), nil
}

// ListOpenActions 列出所有 open 状态的 action
func (s *Store) ListOpenActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListActionsByState(ctx, taskID, StateOpen)
}

// ListCompletedActions 列出所有 done 状态的 action
func (s *Store) ListCompletedActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListActionsByState(ctx, taskID, StateDone)
}

// ListAllActions 列出所有 action
func (s *Store) ListAllActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListNodesByKind(ctx, taskID, core.KindAction)
}

// ListObservationsByConfidence 按验证裁决列出 observation（如轮询
// confidence=unverified 的待裁决假设）。limit<=0 表示不限制。
func (s *Store) ListObservationsByConfidence(ctx context.Context, taskID string, confidence Confidence, limit int) ([]Node, error) {
	query := core.GraphNodeQuery{
		Kind: string(core.KindObservation),
		Filters: map[string]interface{}{
			"task_id":            taskID,
			"metadata.confidence": string(confidence),
		},
		OrderBy: "created_at",
		Limit:   limit,
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, err
	}

	return s.convertGraphNodesToNodes(nodes), nil
}

// ListActionStates 返回 task 下全部 action 的 id → state 投影。
// 依赖判定只需要状态，不需要内容载荷——投影查询避免整图拉取。
func (s *Store) ListActionStates(ctx context.Context, taskID string) (map[string]State, error) {
	if s.pool != nil {
		rows, err := s.pool.Query(ctx, `
			SELECT id, state FROM exploration_node
			WHERE task_id = $1 AND kind = 'action'
		`, taskID)
		if err != nil {
			return nil, fmt.Errorf("query action states: %w", err)
		}
		defer rows.Close()

		states := make(map[string]State)
		for rows.Next() {
			var id string
			var state *string
			if err := rows.Scan(&id, &state); err != nil {
				return nil, fmt.Errorf("scan action state: %w", err)
			}
			if state != nil {
				states[id] = State(*state)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate action states: %w", err)
		}
		return states, nil
	}

	// 内存后端：经通用查询取状态
	nodes, err := s.ListAllActions(ctx, taskID)
	if err != nil {
		return nil, err
	}
	states := make(map[string]State, len(nodes))
	for _, n := range nodes {
		if n.State != nil {
			states[n.ID] = *n.State
		}
	}
	return states, nil
}

// ListResults 列出所有 result 节点
func (s *Store) ListResults(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListNodesByKind(ctx, taskID, core.KindResult)
}

// GetObjective 获取任务的 objective 节点
func (s *Store) GetObjective(ctx context.Context, taskID string) (ObjectiveNode, error) {
	query := core.GraphNodeQuery{
		Kind: string(core.KindObjective),
		Filters: map[string]interface{}{
			"task_id": taskID,
		},
		Limit: 1,
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return ObjectiveNode{}, err
	}

	if len(nodes) == 0 {
		return ObjectiveNode{}, fmt.Errorf("objective not found for task %s", taskID)
	}

	// 转换为 ObjectiveNode
	var content struct {
		Description string                 `json:"description"`
		Context     map[string]interface{} `json:"context,omitempty"`
	}
	if err := json.Unmarshal(nodes[0].Content, &content); err != nil {
		return ObjectiveNode{}, fmt.Errorf("unmarshal objective: %w", err)
	}

	obj := ObjectiveNode{
		ID:   nodes[0].ID,
		Goal: content.Description,
	}

	return obj, nil
}

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

		if len(ge.Metadata) > 0 {
			if attrs, err := json.Marshal(ge.Metadata); err == nil {
				edge.Attrs = attrs
			}
		}

		edges = append(edges, edge)
	}
	return edges
}

// ─────────────────────────────────────────────
// 验证审计链
// ─────────────────────────────────────────────

// RecordVerification 将一次复现验证落 exploration_verification 审计链。
// 坐实与证伪都落档（证伪不进图但证据留档供审计/复盘）。
func (s *Store) RecordVerification(ctx context.Context, v Verification) (string, error) {
	if s.pool == nil {
		return "", fmt.Errorf("RecordVerification requires a pool-backed store")
	}
	if v.ID == "" {
		return "", fmt.Errorf("verification ID is required")
	}
	if v.TaskID == "" || v.NodeID == "" {
		return "", fmt.Errorf("verification task_id/node_id is required")
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO exploration_verification (id, task_id, node_id, primitives, outcome, evidence, duration_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		v.ID, v.TaskID, v.NodeID, v.Primitives, string(v.Outcome), v.Evaluation, v.DurationMs, v.CreatedAt)
	if err != nil {
		return "", fmt.Errorf("insert verification: %w", err)
	}

	return v.ID, nil
}

// ─────────────────────────────────────────────
// HTTP API 专用方法
// ─────────────────────────────────────────────

// ListNodesForAPI 按 task_id 和可选 kind 查询节点（HTTP API 专用）
func (s *Store) ListNodesForAPI(ctx context.Context, taskID string, kind string) ([]Node, error) {
	query := core.GraphNodeQuery{
		Filters: map[string]interface{}{
			"task_id": taskID,
		},
		OrderBy: "created_at",
	}

	// 可选的 kind 过滤
	if kind != "" {
		query.Kind = kind
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	return s.convertGraphNodesToNodes(nodes), nil
}

// ListEdgesForAPI 按 task_id 查询边（HTTP API 专用）。
// 边查询按 task_id 列过滤（有索引），不做全表拉取后内存过滤。
func (s *Store) ListEdgesForAPI(ctx context.Context, taskID string) ([]Edge, error) {
	graphEdges, err := s.graphStore.ListEdges(ctx, core.GraphEdgeQuery{TaskID: taskID})
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
	}

	return s.convertGraphEdgesToEdges(graphEdges, taskID), nil
}

// GetStatsForAPI 返回节点类型统计（e2e 轮询专用）。
// 聚合下推到数据库（GROUP BY），不拉取节点载荷。
func (s *Store) GetStatsForAPI(ctx context.Context, taskID string) (map[string]int, error) {
	stats := map[string]int{
		"objectives":   0,
		"actions":      0,
		"observations": 0,
		"results":      0,
	}

	if s.pool != nil {
		rows, err := s.pool.Query(ctx, `
			SELECT kind, COUNT(*) FROM exploration_node
			WHERE task_id = $1
			GROUP BY kind
		`, taskID)
		if err != nil {
			return nil, fmt.Errorf("query node stats: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var kind string
			var count int
			if err := rows.Scan(&kind, &count); err != nil {
				return nil, fmt.Errorf("scan node stats: %w", err)
			}
			if _, ok := stats[kind]; ok {
				stats[kind] = count
			}
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("iterate node stats: %w", err)
		}
		return stats, nil
	}

	// 内存后端：通用查询后在内存计数
	nodes, err := s.ListNodesForAPI(ctx, taskID, "")
	if err != nil {
		return nil, err
	}
	for _, node := range nodes {
		switch node.Kind {
		case core.KindObjective:
			stats["objectives"]++
		case core.KindAction:
			stats["actions"]++
		case core.KindObservation:
			stats["observations"]++
		case core.KindResult:
			stats["results"]++
		}
	}
	return stats, nil
}

// RecoverOrphanedActions 恢复孤儿 action（runner 重启时调用）
//
// 将所有处于 running 状态的 action 重置为 failed，原因是 runner 重启导致执行中断。
// 这防止了 action 永远停留在 running 状态而无法被重新调度。
func (s *Store) RecoverOrphanedActions(ctx context.Context) (int, error) {
	if s.pool == nil {
		return 0, fmt.Errorf("RecoverOrphanedActions requires direct DB access (pool is nil)")
	}

	query := `
		UPDATE exploration_node
		SET
			state = 'failed',
			metadata = COALESCE(metadata, '{}'::jsonb) || jsonb_build_object(
				'failure_reason', 'Runner 重启导致执行中断',
				'recovered_at', NOW()
			)
		WHERE kind = 'action' AND state = 'running'
		RETURNING id
	`

	rows, err := s.pool.Query(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("update orphaned actions: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return count, fmt.Errorf("scan recovered action id: %w", err)
		}
		count++
	}

	if err := rows.Err(); err != nil {
		return count, fmt.Errorf("iterate recovered actions: %w", err)
	}

	return count, nil
}
