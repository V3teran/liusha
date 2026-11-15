//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0221 uint = 221

// 迁移 0221：节点的状态原因统一叫 state_reason（原 blocked_reason——系统里没有「阻塞」
// 状态，它记的是失败、中止、停摆的原因）。
func TestMigration0221_StateReason(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0221)
	assignment := SeedAssignment(t, pool)
	var taskID, nodeID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'action', 'aborted', '{}', '{"blocked_reason":"偏离目标"}')
		RETURNING id`, taskID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0221)

	var metadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM exploration_node WHERE id = $1`, nodeID).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata != `{"state_reason": "偏离目标"}` {
		t.Errorf("metadata = %s", metadata)
	}
}
