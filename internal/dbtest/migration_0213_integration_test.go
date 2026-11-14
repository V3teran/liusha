//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0213 uint = 213

// 迁移 0213：run 报告的统计改为图上计数，字段改名——时间线终态消息的 promoted /
// attempts 改为 results / hypotheses；agent_run.result 的 Steps / Promoted / Attempts
// 改为 actions / results / hypotheses。数值原样保留，其余字段不动。
func TestMigration0213_ReportFieldNames(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0213)
	assignment := SeedAssignment(t, pool)
	var taskID, convID, msgID, runID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO conversation DEFAULT VALUES RETURNING id`).Scan(&convID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO message (conversation_id, role, kind, metadata)
		VALUES ($1, 'system', 'event',
		        '{"kind":"task_status","status":"completed","steps":11,"actions":4,"promoted":2,"attempts":3,"stop_reason":"task_converged"}')
		RETURNING id`, convID).Scan(&msgID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO agent_run (role, task_id, status, result)
		VALUES ('planner', $1, 'completed', '{"Steps":4,"Promoted":2,"Attempts":3,"StopReason":"task_converged"}')
		RETURNING id`, taskID).Scan(&runID); err != nil {
		t.Fatal(err)
	}

	MigrateUp(t, pool)

	var meta, result map[string]any
	if err := pool.QueryRow(ctx, `SELECT metadata FROM message WHERE id = $1`, msgID).Scan(&meta); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `SELECT result FROM agent_run WHERE id = $1`, runID).Scan(&result); err != nil {
		t.Fatal(err)
	}
	wantMeta := map[string]any{"kind": "task_status", "status": "completed", "steps": float64(11), "actions": float64(4),
		"results": float64(2), "hypotheses": float64(3), "stop_reason": "task_converged"}
	wantResult := map[string]any{"actions": float64(4), "results": float64(2), "hypotheses": float64(3), "stop_reason": "task_converged"}
	if !jsonEqual(meta, wantMeta) {
		t.Errorf("message.metadata = %v，期望 %v", meta, wantMeta)
	}
	if !jsonEqual(result, wantResult) {
		t.Errorf("agent_run.result = %v，期望 %v", result, wantResult)
	}
}
