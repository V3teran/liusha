package explorationgraph

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
)

// ─────────────────────────────────────────────
// HTTP API 专用方法
// ─────────────────────────────────────────────

// taskIDByConversation 把 conversation_id 解析为关联扫描任务的 task_id
// （探索图数据按 task_id 键控，会话经 conversation.task_id 一对一关联）。
// 会话不存在时返回 not-found 语义错误。
func (s *Store) taskIDByConversation(ctx context.Context, conversationID string) (string, error) {
	if s.pool == nil {
		return "", fmt.Errorf("conversation lookup requires direct DB access (pool is nil)")
	}
	var taskID string
	err := s.pool.QueryRow(ctx, `SELECT task_id FROM conversation WHERE id = $1::uuid`, conversationID).Scan(&taskID)
	if err != nil {
		return "", fmt.Errorf("conversation %q: %w", conversationID, err)
	}
	return taskID, nil
}

// ListNodesByConversationID 按 conversation_id 查询节点（经会话关联的 task）。
func (s *Store) ListNodesByConversationID(ctx context.Context, conversationID string, kind string) ([]Node, error) {
	taskID, err := s.taskIDByConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return s.ListNodesForAPI(ctx, taskID, kind)
}

// ListEdgesByConversationID 按 conversation_id 查询边（经会话关联的 task）。
func (s *Store) ListEdgesByConversationID(ctx context.Context, conversationID string) ([]Edge, error) {
	taskID, err := s.taskIDByConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return s.ListEdgesForAPI(ctx, taskID)
}

// ListNodesForAPI 按 task_id 和可选 kind 查询节点（会话解析后的内部查询路径）
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
