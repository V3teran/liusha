//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0223 uint = 223

// 迁移 0223：「中止」拆为已中止与已否决。带否决理由的 aborted 动作改为 rejected（否决方
// monitor）；任务收口的中止（原因「任务已完成」「任务已终止」或空）保持 aborted；自定义
// 监察章程里的旧决策名改写。
func TestMigration0223_RejectedActions(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0223)
	assignment := SeedAssignment(t, pool)
	var taskID string
	if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief) VALUES ($1, 'test') RETURNING id`,
		assignment).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	insert := func(metadata string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
			VALUES (gen_random_uuid(), $1::uuid, 'action', 'aborted', '{}', $2::jsonb)
			RETURNING id`, taskID, metadata).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	vetoed := insert(`{"state_reason":"与已完成动作重复"}`)
	cutOff := insert(`{"state_reason":"任务已终止"}`)
	unknown := insert(`{}`)
	if _, err := pool.Exec(ctx, `INSERT INTO agent_override (agent_code, fields) VALUES ('monitor', '{"charter":{"value":"必要时 kill_action"}}')`); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0223)

	state := func(id string) (string, string) {
		var st, by string
		if err := pool.QueryRow(ctx, `SELECT state, coalesce(metadata->>'rejected_by', '') FROM exploration_node WHERE id = $1`, id).Scan(&st, &by); err != nil {
			t.Fatal(err)
		}
		return st, by
	}
	if st, by := state(vetoed); st != "rejected" || by != "monitor" {
		t.Errorf("监察者否决应改为 rejected/monitor，得 %s/%s", st, by)
	}
	for _, id := range []string{cutOff, unknown} {
		if st, by := state(id); st != "aborted" || by != "" {
			t.Errorf("收口中止应保持 aborted，得 %s/%s", st, by)
		}
	}
	var fields string
	if err := pool.QueryRow(ctx, `SELECT fields->'charter'->>'value' FROM agent_override WHERE agent_code = 'monitor'`).Scan(&fields); err != nil {
		t.Fatal(err)
	}
	if fields != "必要时 reject_action" {
		t.Errorf("自定义章程旧决策名未改写：%s", fields)
	}
}
