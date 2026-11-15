//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0211 uint = 211

// 迁移 0211：任务图跟随任务状态。已结束任务里残留的 open 工作按任务结局收口；
// 根目标的 failed 不再存在（根目标跟随任务，任务只有 completed / aborted）。
func TestMigration0211_SettleGraphsOfFinishedTasks(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0211)
	assignment := SeedAssignment(t, pool)

	newTask := func(status string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief, status) VALUES ($1, 'test', $2) RETURNING id`,
			assignment, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	insert := func(taskID, kind, state, source string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO exploration_node (id, task_id, kind, state, metadata)
			VALUES (gen_random_uuid(), $1::uuid, $2, $3, jsonb_build_object('task_id', $1::text, 'source_type', $4::text))
			RETURNING id`, taskID, kind, state, source).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}

	completed, aborted, active := newTask("completed"), newTask("aborted"), newTask("active")
	cases := []struct {
		name, id, want string
	}{
		{"完成任务的根目标", insert(completed, "objective", "open", "user"), "completed"},
		{"完成任务的派生目标", insert(completed, "objective", "open", "planner"), "exhausted"},
		{"终止任务的根目标", insert(aborted, "objective", "open", "user"), "aborted"},
		{"终止任务里失败的根目标", insert(aborted, "objective", "failed", "user"), "aborted"},
		{"终止任务的派生目标", insert(aborted, "objective", "open", "planner"), "aborted"},
		{"终止任务的 open 动作", insert(aborted, "action", "open", "planner"), "aborted"},
		{"终止任务的已完成动作", insert(aborted, "action", "completed", "planner"), "completed"},
		{"进行中任务不动", insert(active, "action", "open", "planner"), "open"},
	}

	MigrateTo(t, pool, migration0211)

	for _, c := range cases {
		var state string
		if err := pool.QueryRow(ctx, `SELECT state FROM exploration_node WHERE id = $1`, c.id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state != c.want {
			t.Errorf("%s：state = %s，期望 %s", c.name, state, c.want)
		}
	}
}
