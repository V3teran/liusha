//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0212 uint = 212

// 迁移 0212：监察者的重规划请求直达规划者、不再写进图，存量目标内容里的
// monitor_request 删除，其余字段保留。
func TestMigration0212_DropMonitorRequest(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0212)
	assignment := SeedAssignment(t, pool)
	var taskID, nodeID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'objective', 'open',
		        '{"description":"d","monitor_request":{"monitor_replan_request":{"reason":"r"}}}',
		        jsonb_build_object('task_id', $1::text))
		RETURNING id`, taskID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}

	MigrateUp(t, pool)

	var content string
	if err := pool.QueryRow(ctx, `SELECT content::text FROM exploration_node WHERE id = $1`, nodeID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != `{"description": "d"}` {
		t.Errorf("content = %s", content)
	}
}
