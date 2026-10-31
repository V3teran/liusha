package explorationgraph

import (
	"context"
	"fmt"
)

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

// ListVerificationsByTask 按任务读验证审计链（时间倒序）——审计链的读路径：
// 每条记录的 node_id 指向被裁决的假设节点，前端/复盘据此把坐实与证伪的
// 机器证据回挂到图节点上。
func (s *Store) ListVerificationsByTask(ctx context.Context, taskID string, limit int) ([]Verification, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("ListVerificationsByTask requires a pool-backed store")
	}
	if taskID == "" {
		return nil, fmt.Errorf("verification task_id is required")
	}
	if limit <= 0 {
		limit = 200
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, task_id::text, node_id::text, primitives, outcome, evidence, duration_ms, created_at
		FROM exploration_verification
		WHERE task_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT $2`, taskID, limit)
	if err != nil {
		return nil, fmt.Errorf("list verifications: %w", err)
	}
	defer rows.Close()

	out := make([]Verification, 0)
	for rows.Next() {
		var v Verification
		if err := rows.Scan(&v.ID, &v.TaskID, &v.NodeID, &v.Primitives, &v.Outcome, &v.Evaluation, &v.DurationMs, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan verification: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ListVerificationsByConversationID 按会话解析任务后读验证审计链（HTTP API 路径）。
func (s *Store) ListVerificationsByConversationID(ctx context.Context, conversationID string, limit int) ([]Verification, error) {
	taskID, err := s.taskIDByConversation(ctx, conversationID)
	if err != nil {
		return nil, err
	}
	return s.ListVerificationsByTask(ctx, taskID, limit)
}
