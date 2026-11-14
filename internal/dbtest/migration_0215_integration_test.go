//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0215 uint = 215

// 迁移 0215：观察不再内嵌执行轨迹，存量 content.execution_trace 删除，其余字段保留。
func TestMigration0215_DropExecutionTrace(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0215)
	assignment := SeedAssignment(t, pool)
	var taskID, nodeID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'observation',
		        '{"statement":"s","category":"entity","execution_trace":{"traffic":[{"seq":0}],"session":{},"meta":{}}}',
		        jsonb_build_object('task_id', $1::text))
		RETURNING id`, taskID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0215)

	var content string
	if err := pool.QueryRow(ctx, `SELECT content::text FROM exploration_node WHERE id = $1`, nodeID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != `{"category": "entity", "statement": "s"}` {
		t.Errorf("content = %s", content)
	}
}
