//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0218 uint = 218

// 迁移 0218：节点的任务归属只在 task_id 列一份，元数据里重复的 task_id 删除。
func TestMigration0218_DropMetadataTaskID(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0218)
	assignment := SeedAssignment(t, pool)
	var taskID, nodeID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'observation', '{}', jsonb_build_object('task_id', $1::text, 'priority', 'high'))
		RETURNING id`, taskID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0218)

	var metadata string
	if err := pool.QueryRow(ctx, `SELECT metadata::text FROM exploration_node WHERE id = $1`, nodeID).Scan(&metadata); err != nil {
		t.Fatal(err)
	}
	if metadata != `{"priority": "high"}` {
		t.Errorf("metadata = %s", metadata)
	}
}
