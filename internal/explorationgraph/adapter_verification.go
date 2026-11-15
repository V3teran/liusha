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
		INSERT INTO exploration_verification (id, task_id, node_id, repro, outcome, evidence, duration_ms, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		v.ID, v.TaskID, v.NodeID, v.Repro, string(v.Outcome), v.Evidence, v.DurationMs, v.CreatedAt)
	if err != nil {
		return "", fmt.Errorf("insert verification: %w", err)
	}

	return v.ID, nil
}

// ListVerificationsByTask 按任务读验证审计链（时间倒序）——审计链的读路径：
// 每条记录的 node_id 指向被验证的假设节点，前端/复盘据此把坐实与证伪的
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
		SELECT id, task_id::text, node_id::text, repro, outcome, evidence, duration_ms, created_at
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
		if err := rows.Scan(&v.ID, &v.TaskID, &v.NodeID, &v.Repro, &v.Outcome, &v.Evidence, &v.DurationMs, &v.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan verification: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// ConfirmingVerification 取坐实这个结果的那次验证：结果 ←generates— 假设，该假设在结果建立
// 之前最近一次「坐实」判定。结果节点不存在或追溯不到验证时返回包裹 pgx.ErrNoRows 的错误。
// 补交付（坐实了却没进台账的结果）据此重建与正常交付相同的载荷。
func (s *Store) ConfirmingVerification(ctx context.Context, resultID string) (Verification, error) {
	if s.pool == nil {
		return Verification{}, fmt.Errorf("ConfirmingVerification requires a pool-backed store")
	}
	var v Verification
	err := s.pool.QueryRow(ctx, `
		SELECT v.id, v.task_id::text, v.node_id::text, v.repro, v.outcome, v.evidence, v.duration_ms, v.created_at
		FROM exploration_node r
		JOIN exploration_edge e ON e.dst_id = r.id AND e.rel = 'generates'
		JOIN exploration_verification v ON v.node_id = e.src_id AND v.outcome = 'confirmed' AND v.created_at <= r.created_at
		WHERE r.id = $1::uuid AND r.kind = 'result'
		ORDER BY v.created_at DESC
		LIMIT 1`, resultID).Scan(&v.ID, &v.TaskID, &v.NodeID, &v.Repro, &v.Outcome, &v.Evidence, &v.DurationMs, &v.CreatedAt)
	if err != nil {
		return Verification{}, fmt.Errorf("坐实结果 %s 的验证: %w", resultID, err)
	}
	return v, nil
}
