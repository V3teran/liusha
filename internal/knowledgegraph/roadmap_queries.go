package knowledgegraph

import (
	"context"
	"fmt"
)

// ListActionsByRoadmapStep 列出某个 RoadmapStep 派发的所有 Action
//
// 用途：
// - 判断某个 Step 是否完成（检查所有关联的 Action 状态）
// - 统计某个 Step 派发了多少个 Action
func (s *Store) ListActionsByRoadmapStep(ctx context.Context, taskID string, step float64) ([]Node, error) {
	query := `
		SELECT id, task_id, kind, content,
		       state, complexity, depends_on, blocked_reason, roadmap_step,
		       confidence,
		       priority, owner, source_type, source_id, tags, metadata,
		       created_at, updated_at, completed_at
		FROM wm_node
		WHERE task_id = $1 AND kind = $2 AND roadmap_step = $3
		ORDER BY created_at ASC
	`

	rows, err := s.pool.Query(ctx, query, taskID, KindAction, step)
	if err != nil {
		return nil, fmt.Errorf("list actions by roadmap step: %w", err)
	}
	defer rows.Close()

	var nodes []Node
	for rows.Next() {
		var node Node
		err := rows.Scan(
			&node.ID, &node.TaskID, &node.Kind, &node.Content,
			&node.State, &node.Complexity, &node.DependsOn, &node.BlockedReason, &node.RoadmapStep,
			&node.Confidence,
			&node.Priority, &node.Owner, &node.SourceType, &node.SourceID, &node.Tags, &node.Metadata,
			&node.CreatedAt, &node.UpdatedAt, &node.CompletedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("scan action: %w", err)
		}
		nodes = append(nodes, node)
	}

	return nodes, rows.Err()
}

// IsRoadmapStepComplete 判断某个 RoadmapStep 是否完成
//
// 规则：
// - 所有关联的 Action 都是 done 状态
// - 如果没有关联的 Action，返回 false（还未派发）
func (s *Store) IsRoadmapStepComplete(ctx context.Context, taskID string, step float64) (bool, error) {
	actions, err := s.ListActionsByRoadmapStep(ctx, taskID, step)
	if err != nil {
		return false, err
	}

	// 没有关联的 Action，说明还未派发
	if len(actions) == 0 {
		return false, nil
	}

	// 检查是否所有 Action 都完成
	for _, action := range actions {
		if action.State == nil || *action.State != StateDone {
			return false, nil
		}
	}

	return true, nil
}

// CountActionsByRoadmapStep 统计某个 RoadmapStep 的 Action 数量（按状态分组）
//
// 返回：
// - map[State]int：各状态的 Action 数量
func (s *Store) CountActionsByRoadmapStep(ctx context.Context, taskID string, step float64) (map[State]int, error) {
	query := `
		SELECT state, COUNT(*)
		FROM wm_node
		WHERE task_id = $1 AND kind = $2 AND roadmap_step = $3
		GROUP BY state
	`

	rows, err := s.pool.Query(ctx, query, taskID, KindAction, step)
	if err != nil {
		return nil, fmt.Errorf("count actions by roadmap step: %w", err)
	}
	defer rows.Close()

	counts := make(map[State]int)
	for rows.Next() {
		var state State
		var count int
		if err := rows.Scan(&state, &count); err != nil {
			return nil, err
		}
		counts[state] = count
	}

	return counts, rows.Err()
}
