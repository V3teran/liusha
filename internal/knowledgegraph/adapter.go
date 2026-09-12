package knowledgegraph

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
)

// AdapterStore 是知识图谱的 Framework 适配器
// 使用 Framework 的 GraphStore 和标准类型
type AdapterStore struct {
	graphStore core.GraphStore
}

// NewAdapterStore 创建知识图谱适配器
func NewAdapterStore(graphStore core.GraphStore) *AdapterStore {
	return &AdapterStore{
		graphStore: graphStore,
	}
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
		Confidence: float64(obs.Confidence),
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
				"confidence": float64(obs.Confidence),
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
		Confidence: float64(result.Confidence),
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
				"confidence": float64(result.Confidence),
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
