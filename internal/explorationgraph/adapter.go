package explorationgraph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AdapterStore 是知识图谱的 Framework 适配器
// 使用 Framework 的 GraphStore 和标准类型
type AdapterStore struct {
	graphStore core.GraphStore
	pool       *pgxpool.Pool // 用于 Roadmap 等直接 SQL 操作（仅在 NewStore 中设置）
}

// NewAdapterStore 创建知识图谱适配器
func NewAdapterStore(graphStore core.GraphStore) *AdapterStore {
	return &AdapterStore{
		graphStore: graphStore,
	}
}

// GraphStore 返回底层的 GraphStore（用于测试）
func (s *AdapterStore) GraphStore() core.GraphStore {
	return s.graphStore
}

// Objective 是业务层的目标数据结构
type Objective struct {
	ID          string
	TaskID      string
	Description string
	CreatedAt   time.Time
}

// Action 是业务层的动作数据结构
type Action struct {
	ID          string
	ObjectiveID string
	Type        string
	Target      string
	Parameters  map[string]interface{}
	State       core.ActionState
	Complexity  core.ActionComplexity
	DependsOn   []string
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// Observation 是业务层的观察数据结构
type Observation struct {
	ID         string
	ActionID   string
	Type       string
	Data       map[string]interface{}
	Confidence core.ObservationConfidence
	CreatedAt  time.Time
}

// Evaluation 是业务层的评估数据结构
type Evaluation struct {
	ID             string
	ObservationID  string
	Outcome        core.EvaluationOutcome
	Reasoning      string
	Confidence     float64
	CreatedAt      time.Time
}

// Result 是业务层的结果数据结构
type Result struct {
	ID            string
	EvaluationID  string
	Type          string
	Data          map[string]interface{}
	Confidence    core.ObservationConfidence
	CreatedAt     time.Time
}

// CreateObjective 创建目标节点
func (s *AdapterStore) CreateObjective(ctx context.Context, obj Objective) error {
	content, err := json.Marshal(obj)
	if err != nil {
		return fmt.Errorf("marshal objective: %w", err)
	}

	node := &core.GraphNode{
		ID:      obj.ID,
		Kind:    string(core.KindObjective),
		Content: content,
		State:   string(core.ActionStateOpen),
		Metadata: map[string]interface{}{
			"task_id":     obj.TaskID,
			"description": obj.Description,
		},
	}

	return s.graphStore.CreateNode(ctx, node)
}

// CreateAction 创建动作节点
func (s *AdapterStore) CreateAction(ctx context.Context, action Action) error {
	content, err := json.Marshal(action)
	if err != nil {
		return fmt.Errorf("marshal action: %w", err)
	}

	node := &core.GraphNode{
		ID:         action.ID,
		Kind:       string(core.KindAction),
		Content:    content,
		State:      string(action.State),
		Confidence: 0.0, // Actions don't have confidence
		Metadata: map[string]interface{}{
			"objective_id": action.ObjectiveID,
			"type":         action.Type,
			"target":       action.Target,
			"complexity":   int(action.Complexity),
			"depends_on":   action.DependsOn,
		},
	}

	if err := s.graphStore.CreateNode(ctx, node); err != nil {
		return err
	}

	// 创建 objective → action 的隐含关系
	if action.ObjectiveID != "" {
		edge := &core.GraphEdge{
			From:     action.ObjectiveID,
			To:       action.ID,
			Relation: string(core.RelationEnables),
			Metadata: map[string]interface{}{
				"type": "objective_to_action",
			},
		}
		_ = s.graphStore.CreateEdge(ctx, edge) // 忽略错误（可能已存在）
	}

	// 创建动作依赖关系
	for _, depID := range action.DependsOn {
		edge := &core.GraphEdge{
			From:     action.ID,
			To:       depID,
			Relation: string(core.RelationDependsOn),
		}
		_ = s.graphStore.CreateEdge(ctx, edge)
	}

	return nil
}

// RecordObservation 记录观察结果
func (s *AdapterStore) RecordObservation(ctx context.Context, obs Observation) error {
	content, err := json.Marshal(obs)
	if err != nil {
		return fmt.Errorf("marshal observation: %w", err)
	}

	node := &core.GraphNode{
		ID:         obs.ID,
		Kind:       string(core.KindObservation),
		Content:    content,
		Confidence: confidenceToFloat(obs.Confidence),
		Metadata: map[string]interface{}{
			"action_id": obs.ActionID,
			"type":      obs.Type,
		},
	}

	if err := s.graphStore.CreateNode(ctx, node); err != nil {
		return err
	}

	// 创建 action → observation 关系
	if obs.ActionID != "" {
		edge := &core.GraphEdge{
			From:     obs.ActionID,
			To:       obs.ID,
			Relation: string(core.RelationGenerates),
			Metadata: map[string]interface{}{
				"confidence": confidenceToFloat(obs.Confidence),
			},
		}
		_ = s.graphStore.CreateEdge(ctx, edge)
	}

	return nil
}

// AddEvaluation 添加评估结论
func (s *AdapterStore) AddEvaluation(ctx context.Context, eval Evaluation) error {
	content, err := json.Marshal(eval)
	if err != nil {
		return fmt.Errorf("marshal evaluation: %w", err)
	}

	node := &core.GraphNode{
		ID:         eval.ID,
		Kind:       string(core.KindEvaluation),
		Content:    content,
		State:      string(eval.Outcome),
		Confidence: eval.Confidence,
		Metadata: map[string]interface{}{
			"observation_id": eval.ObservationID,
			"outcome":        string(eval.Outcome),
			"reasoning":      eval.Reasoning,
		},
	}

	if err := s.graphStore.CreateNode(ctx, node); err != nil {
		return err
	}

	// 根据评估结论创建不同的关系
	if eval.ObservationID != "" {
		var relation core.RelationKind
		if eval.Outcome.IsPositive() {
			relation = core.RelationConfirms
		} else if eval.Outcome.IsNegative() {
			relation = core.RelationRefutes
		} else {
			// uncertain 不创建关系
			return nil
		}

		edge := &core.GraphEdge{
			From:     eval.ID,
			To:       eval.ObservationID,
			Relation: string(relation),
			Metadata: map[string]interface{}{
				"outcome":    string(eval.Outcome),
				"confidence": eval.Confidence,
			},
		}
		_ = s.graphStore.CreateEdge(ctx, edge)
	}

	return nil
}

// ConfirmResult 确认最终结果
func (s *AdapterStore) ConfirmResult(ctx context.Context, result Result) error {
	content, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("marshal result: %w", err)
	}

	node := &core.GraphNode{
		ID:         result.ID,
		Kind:       string(core.KindResult),
		Content:    content,
		Confidence: confidenceToFloat(result.Confidence),
		Metadata: map[string]interface{}{
			"evaluation_id": result.EvaluationID,
			"type":          result.Type,
		},
	}

	if err := s.graphStore.CreateNode(ctx, node); err != nil {
		return err
	}

	// 创建 evaluation → result 关系
	if result.EvaluationID != "" {
		edge := &core.GraphEdge{
			From:     result.EvaluationID,
			To:       result.ID,
			Relation: string(core.RelationConfirms),
			Metadata: map[string]interface{}{
				"confidence": confidenceToFloat(result.Confidence),
			},
		}
		_ = s.graphStore.CreateEdge(ctx, edge)
	}

	return nil
}

// GetOpenActions 获取所有待执行的动作
func (s *AdapterStore) GetOpenActions(ctx context.Context, objectiveID string) ([]*Action, error) {
	query := core.GraphNodeQuery{
		Kind:  string(core.KindAction),
		State: string(core.ActionStateOpen),
	}

	if objectiveID != "" {
		query.Filters = map[string]interface{}{
			"objective_id": objectiveID,
		}
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, err
	}

	actions := make([]*Action, 0, len(nodes))
	for _, node := range nodes {
		var action Action
		if err := json.Unmarshal(node.Content, &action); err != nil {
			continue
		}
		actions = append(actions, &action)
	}

	return actions, nil
}

// GetObservations 获取动作的观察结果
func (s *AdapterStore) GetObservations(ctx context.Context, actionID string) ([]*Observation, error) {
	query := core.GraphNodeQuery{
		Kind: string(core.KindObservation),
		Filters: map[string]interface{}{
			"action_id": actionID,
		},
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, err
	}

	observations := make([]*Observation, 0, len(nodes))
	for _, node := range nodes {
		var obs Observation
		if err := json.Unmarshal(node.Content, &obs); err != nil {
			continue
		}
		observations = append(observations, &obs)
	}

	return observations, nil
}

// UpdateActionState 更新动作状态
func (s *AdapterStore) UpdateActionState(ctx context.Context, actionID string, state core.ActionState) error {
	return s.graphStore.UpdateNode(ctx, actionID, core.GraphNodeUpdate{
		State: string(state),
	})
}

// CompareAndSwapState 原子性地更新节点状态（仅在 expectedState 匹配时）
// 实现 core.GraphStore 接口
func (s *AdapterStore) CompareAndSwapState(ctx context.Context, taskID, nodeID string, expectedState, newState string) (bool, error) {
	return s.graphStore.CompareAndSwapState(ctx, taskID, nodeID, expectedState, newState)
}

// GetActionsByState 根据状态获取动作
func (s *AdapterStore) GetActionsByState(ctx context.Context, state core.ActionState) ([]*Action, error) {
	query := core.GraphNodeQuery{
		Kind:  string(core.KindAction),
		State: string(state),
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, err
	}

	actions := make([]*Action, 0, len(nodes))
	for _, node := range nodes {
		var action Action
		if err := json.Unmarshal(node.Content, &action); err != nil {
			continue
		}
		actions = append(actions, &action)
	}

	return actions, nil
}

// ─────────────────────────────────────────────
// 兼容旧 Store 接口的方法
// ─────────────────────────────────────────────

// CreateNode 创建节点（兼容旧接口）
func (s *AdapterStore) CreateNode(ctx context.Context, node Node) (string, error) {
	// 转换为 Framework GraphNode
	graphNode := &core.GraphNode{
		ID:      node.ID,
		Kind:    string(node.Kind),
		Content: node.Content,
		Metadata: map[string]interface{}{
			"task_id":     node.TaskID,
			"source_type": string(node.SourceType),
			"source_id":   node.SourceID,
			"priority":    node.Priority,
		},
		CreatedAt: node.CreatedAt,
		UpdatedAt: node.UpdatedAt,
	}

	// 添加可选字段
	if node.Owner != "" {
		graphNode.Metadata["owner"] = node.Owner
	}
	if len(node.Tags) > 0 {
		graphNode.Metadata["tags"] = node.Tags
	}
	if len(node.Metadata) > 0 {
		graphNode.Metadata["business_metadata"] = node.Metadata
	}

	// Action 专用字段
	if node.State != nil {
		graphNode.State = string(*node.State)
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
	if node.RoadmapStep != nil {
		graphNode.Metadata["roadmap_step"] = *node.RoadmapStep
	}

	// Confidence 字段
	if node.Confidence != nil {
		switch *node.Confidence {
		case ConfidenceVerified:
			graphNode.Confidence = 1.0
		case ConfidenceUnverified:
			graphNode.Confidence = 0.5
		case ConfidenceRefuted:
			graphNode.Confidence = 0.0
		}
	}

	err := s.graphStore.CreateNode(ctx, graphNode)
	if err != nil {
		return "", err
	}

	return node.ID, nil
}

// GetNode 获取节点（兼容旧接口）
func (s *AdapterStore) GetNode(ctx context.Context, id string) (*Node, error) {
	graphNode, err := s.graphStore.GetNode(ctx, id)
	if err != nil {
		return nil, err
	}

	return s.graphNodeToNode(graphNode)
}

// ListNodesByKind 列出指定 task 和 kind 的节点
func (s *AdapterStore) ListNodesByKind(ctx context.Context, taskID string, kind core.NodeKind) ([]Node, error) {
	query := core.GraphNodeQuery{
		Kind: string(kind),
		Filters: map[string]interface{}{
			"metadata.task_id": taskID,
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
func (s *AdapterStore) ListActionsByState(ctx context.Context, taskID string, state State) ([]Node, error) {
	query := core.GraphNodeQuery{
		Kind:  string(core.KindAction),
		State: string(state),
		Filters: map[string]interface{}{
			"metadata.task_id": taskID,
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
func (s *AdapterStore) ListOpenActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListActionsByState(ctx, taskID, StateOpen)
}

// ListRunningActions 列出所有 running 状态的 action
func (s *AdapterStore) ListRunningActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListActionsByState(ctx, taskID, StateRunning)
}

// ListCompletedActions 列出所有 done 状态的 action
func (s *AdapterStore) ListCompletedActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListActionsByState(ctx, taskID, StateDone)
}

// ListAllActions 列出所有 action
func (s *AdapterStore) ListAllActions(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListNodesByKind(ctx, taskID, core.KindAction)
}

// CreateEdge 创建边（实现 core.GraphStore 接口）
func (s *AdapterStore) CreateEdge(ctx context.Context, edge *core.GraphEdge) error {
	return s.graphStore.CreateEdge(ctx, edge)
}

// CreateBusinessEdge 创建边（业务层接口，使用业务层 Edge 类型）
func (s *AdapterStore) CreateBusinessEdge(ctx context.Context, edge Edge) error {
	graphEdge := &core.GraphEdge{
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

	return s.graphStore.CreateEdge(ctx, graphEdge)
}

// ListEdgesFrom 列出从指定节点出发的边
func (s *AdapterStore) ListEdgesFrom(ctx context.Context, taskID, srcID string) ([]Edge, error) {
	query := core.GraphEdgeQuery{
		From: srcID,
	}

	edges, err := s.graphStore.ListEdges(ctx, query)
	if err != nil {
		return nil, err
	}

	return s.convertGraphEdgesToEdges(edges, taskID), nil
}

// ListEdgesTo 列出指向指定节点的边
func (s *AdapterStore) ListEdgesTo(ctx context.Context, taskID, dstID string) ([]Edge, error) {
	query := core.GraphEdgeQuery{
		To: dstID,
	}

	edges, err := s.graphStore.ListEdges(ctx, query)
	if err != nil {
		return nil, err
	}

	return s.convertGraphEdgesToEdges(edges, taskID), nil
}

// ListEdgesByRelation 列出指定关系类型的边
func (s *AdapterStore) ListEdgesByRelation(ctx context.Context, taskID string, rel Relation) ([]Edge, error) {
	query := core.GraphEdgeQuery{
		Relation: string(rel),
	}

	edges, err := s.graphStore.ListEdges(ctx, query)
	if err != nil {
		return nil, err
	}

	return s.convertGraphEdgesToEdges(edges, taskID), nil
}

// DeleteNode 删除节点
func (s *AdapterStore) DeleteNode(ctx context.Context, id string) error {
	return s.graphStore.DeleteNode(ctx, id)
}

// UpdateNodeConfidence 更新节点置信度
func (s *AdapterStore) UpdateNodeConfidence(ctx context.Context, id string, confidence Confidence) error {
	var conf float64
	switch confidence {
	case ConfidenceVerified:
		conf = 1.0
	case ConfidenceUnverified:
		conf = 0.5
	case ConfidenceRefuted:
		conf = 0.0
	}

	return s.graphStore.UpdateNode(ctx, id, core.GraphNodeUpdate{
		Confidence: &conf,
	})
}

// UpdateActionStateWithReason 更新 action 状态和阻塞原因
func (s *AdapterStore) UpdateActionStateWithReason(ctx context.Context, id string, state State, blockedReason *string) error {
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

// CompareAndSwapActionState 原子性地比较并交换 action 状态（CAS操作）
// 只有当前状态等于 expectedState 时才更新为 newState
// 返回 (true, nil) 表示更新成功
// 返回 (false, nil) 表示状态不匹配，未更新（被其他goroutine抢占）
func (s *AdapterStore) CompareAndSwapActionState(ctx context.Context, taskID, actionID string, expectedState, newState State, blockedReason *string) (bool, error) {
	// 先尝试 CAS（转换为 string）
	ok, err := s.graphStore.CompareAndSwapState(ctx, taskID, actionID, string(expectedState), string(newState))
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil // CAS 失败，状态不匹配
	}

	// CAS 成功，更新 blocked_reason（如果有）
	if blockedReason != nil {
		update := core.GraphNodeUpdate{
			Metadata: map[string]interface{}{
				"blocked_reason": *blockedReason,
			},
		}
		if err := s.graphStore.UpdateNode(ctx, actionID, update); err != nil {
			// 元数据更新失败不影响状态已经更新的事实
			return true, nil
		}
	}

	return true, nil
}

// ─────────────────────────────────────────────
// 辅助方法
// ─────────────────────────────────────────────

// convertGraphNodesToNodes 转换 GraphNode 列表为 Node 列表
func (s *AdapterStore) convertGraphNodesToNodes(graphNodes []*core.GraphNode) []Node {
	nodes := make([]Node, 0, len(graphNodes))
	for _, gn := range graphNodes {
		node, err := s.graphNodeToNode(gn)
		if err != nil {
			continue
		}
		nodes = append(nodes, *node)
	}
	return nodes
}

// graphNodeToNode 转换单个 GraphNode 为 Node
func (s *AdapterStore) graphNodeToNode(graphNode *core.GraphNode) (*Node, error) {
	node := &Node{
		ID:        graphNode.ID,
		Kind:      core.NodeKind(graphNode.Kind),
		Content:   graphNode.Content,
		CreatedAt: graphNode.CreatedAt,
		UpdatedAt: graphNode.UpdatedAt,
	}

	// 从 Metadata 恢复字段
	if taskID, ok := graphNode.Metadata["task_id"].(string); ok {
		node.TaskID = taskID
	}
	if sourceType, ok := graphNode.Metadata["source_type"].(string); ok {
		node.SourceType = SourceType(sourceType)
	}
	if sourceID, ok := graphNode.Metadata["source_id"].(string); ok {
		node.SourceID = sourceID
	}
	if owner, ok := graphNode.Metadata["owner"].(string); ok {
		node.Owner = owner
	}
	if tags, ok := graphNode.Metadata["tags"].([]interface{}); ok {
		strTags := make([]string, 0, len(tags))
		for _, tag := range tags {
			if str, ok := tag.(string); ok {
				strTags = append(strTags, str)
			}
		}
		node.Tags = strTags
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

	// DependsOn
	if dependsOn, ok := graphNode.Metadata["depends_on"].([]interface{}); ok {
		deps := make([]string, 0, len(dependsOn))
		for _, dep := range dependsOn {
			if str, ok := dep.(string); ok {
				deps = append(deps, str)
			}
		}
		node.DependsOn = deps
	}

	// BlockedReason
	if blockedReason, ok := graphNode.Metadata["blocked_reason"].(string); ok {
		node.BlockedReason = &blockedReason
	}

	// RoadmapStep
	if roadmapStep, ok := graphNode.Metadata["roadmap_step"].(float64); ok {
		node.RoadmapStep = &roadmapStep
	}

	// Confidence
	if graphNode.Confidence >= 0.8 {
		conf := ConfidenceVerified
		node.Confidence = &conf
	} else if graphNode.Confidence > 0 {
		conf := ConfidenceUnverified
		node.Confidence = &conf
	}

	return node, nil
}

// convertGraphEdgesToEdges 转换 GraphEdge 列表为 Edge 列表
func (s *AdapterStore) convertGraphEdgesToEdges(graphEdges []*core.GraphEdge, taskID string) []Edge {
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

// ListResults 列出所有 result 节点
func (s *AdapterStore) ListResults(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListNodesByKind(ctx, taskID, core.KindResult)
}

// ListVerifiedResults 列出所有已验证的 result 节点
func (s *AdapterStore) ListVerifiedResults(ctx context.Context, taskID string) ([]Node, error) {
	query := core.GraphNodeQuery{
		Kind:          string(core.KindResult),
		MinConfidence: 0.8, // verified
		Filters: map[string]interface{}{
			"metadata.task_id": taskID,
		},
		OrderBy: "created_at",
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, err
	}

	return s.convertGraphNodesToNodes(nodes), nil
}

// ListObservations 列出所有 observation 节点
func (s *AdapterStore) ListObservations(ctx context.Context, taskID string) ([]Node, error) {
	return s.ListNodesByKind(ctx, taskID, core.KindObservation)
}

// ListUnverifiedObservations 列出所有未验证的 observation 节点
func (s *AdapterStore) ListUnverifiedObservations(ctx context.Context, taskID string) ([]Node, error) {
	query := core.GraphNodeQuery{
		Kind: string(core.KindObservation),
		Filters: map[string]interface{}{
			"metadata.task_id": taskID,
		},
		OrderBy: "created_at",
	}

	nodes, err := s.graphStore.ListNodes(ctx, query)
	if err != nil {
		return nil, err
	}

	// 过滤未验证的
	var unverified []Node
	for _, node := range s.convertGraphNodesToNodes(nodes) {
		if node.Confidence == nil || *node.Confidence == ConfidenceUnverified {
			unverified = append(unverified, node)
		}
	}

	return unverified, nil
}

// GetObjective 获取任务的 objective 节点
func (s *AdapterStore) GetObjective(ctx context.Context, taskID string) (ObjectiveNode, error) {
	query := core.GraphNodeQuery{
		Kind: string(core.KindObjective),
		Filters: map[string]interface{}{
			"metadata.task_id": taskID,
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

// UpdateNode 更新节点（转发到 GraphStore）
func (s *AdapterStore) UpdateNode(ctx context.Context, id string, update core.GraphNodeUpdate) error {
	return s.graphStore.UpdateNode(ctx, id, update)
}

// RecordVerification 记录验证结果
func (s *AdapterStore) RecordVerification(ctx context.Context, v Verification) (string, error) {
	if v.ID == "" {
		return "", fmt.Errorf("verification ID is required")
	}

	// 创建验证节点（作为审计链）
	content, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("marshal verification: %w", err)
	}

	node := &core.GraphNode{
		ID:      v.ID,
		Kind:    "verification",
		Content: content,
		Metadata: map[string]interface{}{
			"task_id":     v.TaskID,
			"node_id":     v.NodeID,
			"outcome":     v.Outcome,
			"duration_ms": v.DurationMs,
		},
		CreatedAt: v.CreatedAt,
	}

	if err := s.graphStore.CreateNode(ctx, node); err != nil {
		return "", fmt.Errorf("create verification node: %w", err)
	}

	return v.ID, nil
}

// ListEdges 查询边列表（转发到 GraphStore）
func (s *AdapterStore) ListEdges(ctx context.Context, query core.GraphEdgeQuery) ([]*core.GraphEdge, error) {
	return s.graphStore.ListEdges(ctx, query)
}

// DeleteEdge 删除边（转发到 GraphStore）
func (s *AdapterStore) DeleteEdge(ctx context.Context, from, to, relation string) error {
	return s.graphStore.DeleteEdge(ctx, from, to, relation)
}

// confidenceToFloat 将 ObservationConfidence 转换为 float64
func confidenceToFloat(c core.ObservationConfidence) float64 {
	switch c {
	case core.ConfidenceVerified:
		return 0.9
	case core.ConfidenceUnverified:
		return 0.5
	case core.ConfidenceRefuted:
		return 0.1
	default:
		return 0.0
	}
}

// ─────────────────────────────────────────────
// HTTP API 专用方法（Phase 1: 知识图谱 API）
// ─────────────────────────────────────────────

// ListNodesForAPI 按 task_id 和可选 kind 查询节点（HTTP API 专用）
func (s *AdapterStore) ListNodesForAPI(ctx context.Context, taskID string, kind string) ([]Node, error) {
	query := core.GraphNodeQuery{
		Filters: map[string]interface{}{
			"metadata.task_id": taskID,
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

// ListEdgesForAPI 按 task_id 查询边（HTTP API 专用）
func (s *AdapterStore) ListEdgesForAPI(ctx context.Context, taskID string) ([]Edge, error) {
	// GraphEdgeQuery 没有 Filters 字段，需要先查所有边再过滤
	// 或者通过查询所有节点的边来实现
	query := core.GraphEdgeQuery{
		// 空查询返回所有边
		Limit: 10000, // 设置一个较大的上限
	}

	graphEdges, err := s.graphStore.ListEdges(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
	}

	// 过滤出属于指定 task 的边（通过节点关联）
	// 先获取该 task 的所有节点 ID
	nodes, err := s.ListNodesForAPI(ctx, taskID, "")
	if err != nil {
		return nil, fmt.Errorf("list nodes for filtering: %w", err)
	}

	nodeIDs := make(map[string]bool)
	for _, node := range nodes {
		nodeIDs[node.ID] = true
	}

	// 只保留源节点或目标节点属于该 task 的边
	filteredEdges := make([]*core.GraphEdge, 0)
	for _, edge := range graphEdges {
		if nodeIDs[edge.From] || nodeIDs[edge.To] {
			filteredEdges = append(filteredEdges, edge)
		}
	}

	return s.convertGraphEdgesToEdges(filteredEdges, taskID), nil
}

// GetStatsForAPI 返回节点类型统计（e2e 轮询专用）
func (s *AdapterStore) GetStatsForAPI(ctx context.Context, taskID string) (map[string]int, error) {
	// 查询所有节点
	nodes, err := s.ListNodesForAPI(ctx, taskID, "")
	if err != nil {
		return nil, err
	}

	// 按类型统计
	stats := map[string]int{
		"objectives":   0,
		"actions":      0,
		"observations": 0,
		"evaluations":  0,
		"results":      0,
	}

	for _, node := range nodes {
		switch node.Kind {
		case core.KindObjective:
			stats["objectives"]++
		case core.KindAction:
			stats["actions"]++
		case core.KindObservation:
			stats["observations"]++
		case core.KindEvaluation:
			stats["evaluations"]++
		case core.KindResult:
			stats["results"]++
		}
	}

	return stats, nil
}
