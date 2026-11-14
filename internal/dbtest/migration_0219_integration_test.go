//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0219 uint = 219

// 迁移 0219：节点不再记 source_id（没有任何读者，四处写法各不相同），存量删除。
func TestMigration0219_DropSourceID(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0219)
	assignment := SeedAssignment(t, pool)
	var taskID, nodeID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'result', '{}', '{"source_type":"evaluator","source_id":"ver-1"}')
		RETURNING id`, taskID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0219)

	var metadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM exploration_node WHERE id = $1`, nodeID).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata != `{"source_type": "evaluator"}` {
		t.Errorf("metadata = %s", metadata)
	}
}
