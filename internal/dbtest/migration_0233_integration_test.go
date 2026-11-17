//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0233 uint = 233

// 迁移 0233：动作不再携带类型（固定阶段词表本身是某个领域的方法论），存量动作节点去掉
// content.type；其它节点类型的同名键不动。
func TestMigration0233_ActionDropType(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0233)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, goal) VALUES ($1, 'x') RETURNING id`, assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	insert := func(kind, content string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO exploration_node (id, task_id, kind, state, content)
			VALUES (gen_random_uuid(), $1, $2, CASE WHEN $2 = 'action' THEN 'open' END, $3::jsonb) RETURNING id::text`,
			taskID, kind, content).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	cases := []struct{ id, want string }{
		{insert("action", `{"type":"testing","instruction":"i","reasoning":"r"}`), `{"reasoning": "r", "instruction": "i"}`},
		{insert("action", `{"instruction":"无类型"}`), `{"instruction": "无类型"}`},
		{insert("observation", `{"type":"keep"}`), `{"type": "keep"}`},
	}

	MigrateTo(t, pool, migration0233)

	for _, c := range cases {
		var content string
		if err := pool.QueryRow(ctx, `SELECT content::text FROM exploration_node WHERE id = $1`, c.id).Scan(&content); err != nil {
			t.Fatal(err)
		}
		if content != c.want {
			t.Errorf("content = %s，期望 %s", content, c.want)
		}
	}
}
