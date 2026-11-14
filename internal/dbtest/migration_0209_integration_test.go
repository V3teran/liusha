//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0209 uint = 209

// 迁移 0209：动作之间不再有执行依赖，存量动作 metadata 与 content 里的 depends_on
// 一并删除，其余键原样保留。
func TestMigration0209_DropActionDependsOn(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0209)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}

	var actionID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'action', 'open',
		        '{"instruction":"i","depends_on":["x"],"triggered_by":[]}',
		        jsonb_build_object('task_id', $1::text, 'priority', 'high', 'depends_on', '["x"]'::jsonb))
		RETURNING id`, taskID).Scan(&actionID); err != nil {
		t.Fatal(err)
	}

	MigrateUp(t, pool)

	var content, metadata map[string]any
	if err := pool.QueryRow(ctx, `SELECT content, metadata FROM exploration_node WHERE id = $1`, actionID).
		Scan(&content, &metadata); err != nil {
		t.Fatal(err)
	}
	if _, ok := content["depends_on"]; ok {
		t.Errorf("content 仍带 depends_on: %v", content)
	}
	if _, ok := metadata["depends_on"]; ok {
		t.Errorf("metadata 仍带 depends_on: %v", metadata)
	}
	if content["instruction"] != "i" || metadata["priority"] != "high" || metadata["task_id"] != taskID {
		t.Errorf("其余键应保留: content=%v metadata=%v", content, metadata)
	}
}
