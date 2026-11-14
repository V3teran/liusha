//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0214 uint = 214

// 迁移 0214：优先级只在节点层存一份。观察与结果内容里的 priority 归一后写进节点
// metadata.priority（大小写、空白、未知值按 NormalizePriority 同口径），内容里删除。
func TestMigration0214_PriorityOnNode(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0214)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	insert := func(kind, content string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO exploration_node (id, task_id, kind, content, metadata)
			VALUES (gen_random_uuid(), $1::uuid, $2, $3::jsonb, jsonb_build_object('task_id', $1::text, 'priority', 'medium'))
			RETURNING id`, taskID, kind, content).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	cases := []struct{ name, id, want string }{
		{"观察 Critical", insert("observation", `{"statement":"s","priority":" Critical "}`), "critical"},
		{"结果 high", insert("result", `{"summary":"s","priority":"high"}`), "high"},
		{"未知取值归 medium", insert("observation", `{"statement":"s","priority":"urgent"}`), "medium"},
		{"无优先级保持原值", insert("observation", `{"statement":"s"}`), "medium"},
	}

	MigrateTo(t, pool, migration0214)

	for _, c := range cases {
		var priority string
		var hasContentPriority bool
		if err := pool.QueryRow(ctx, `SELECT metadata->>'priority', content ? 'priority' FROM exploration_node WHERE id = $1`, c.id).
			Scan(&priority, &hasContentPriority); err != nil {
			t.Fatal(err)
		}
		if priority != c.want || hasContentPriority {
			t.Errorf("%s：priority = %s（期望 %s），内容仍带 priority = %v", c.name, priority, c.want, hasContentPriority)
		}
	}
}
