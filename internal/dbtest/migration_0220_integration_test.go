//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0220 uint = 220

// 迁移 0220：结果的陈述与假设同名（statement），存量结果内容的 summary 改名。
func TestMigration0220_ResultStatement(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0220)
	assignment := SeedAssignment(t, pool)
	var taskID, nodeID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'result', '{"summary":"s","impact":"i"}', '{}')
		RETURNING id`, taskID).Scan(&nodeID); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0220)

	var content string
	if err := pool.QueryRow(ctx, `SELECT content::text FROM exploration_node WHERE id = $1`, nodeID).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if content != `{"impact": "i", "statement": "s"}` {
		t.Errorf("content = %s", content)
	}
}
