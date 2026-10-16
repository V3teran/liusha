package explorationgraph

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

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
			"task_id":             taskID,
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
