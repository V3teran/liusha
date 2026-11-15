//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0217 uint = 217

// 迁移 0217：动作内容只留 type / instruction / reasoning。优先级与复杂度在节点层，
// 因果在 triggers 边，规划理由统一叫 reasoning（原 reason），无读者的 metadata 与
// origin 删除。
func TestMigration0217_ActionContent(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0217)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	insert := func(content string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
			VALUES (gen_random_uuid(), $1::uuid, 'action', 'open', $2::jsonb, jsonb_build_object('task_id', $1::text))
			RETURNING id`, taskID, content).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	cases := []struct{ id, want string }{
		{insert(`{"type":"testing","instruction":"i","reason":"r","complexity":"simple","priority":"high","triggered_by":[],"metadata":{}}`),
			`{"type": "testing", "reasoning": "r", "instruction": "i"}`},
		{insert(`{"instruction":"延续","reasoning":"r2"}`), `{"reasoning": "r2", "instruction": "延续"}`},
		{insert(`{"type":"testing","instruction":"人工","origin":"control-plane"}`), `{"type": "testing", "instruction": "人工"}`},
	}

	MigrateTo(t, pool, migration0217)

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
