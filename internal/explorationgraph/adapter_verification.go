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

