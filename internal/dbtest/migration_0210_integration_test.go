//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0210 uint = 210

// 迁移 0210：中断不是失败。存量里因 run 被打断（ctx 取消、runner 重启）记成 failed 的
// 动作改正：任务仍 active 的退回 open 等下一个 run 重做，已结束任务的落 aborted；
// 停摆判死（retryable 标记）仍是失败，只去掉废弃的标记键。
func TestMigration0210_InterruptedActionsAreNotFailures(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0210)
	assignment := SeedAssignment(t, pool)

	newTask := func(status string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief, status) VALUES ($1, 'test', $2) RETURNING id`,
			assignment, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	active, aborted := newTask("active"), newTask("aborted")

	insert := func(taskID, metadata string) string {
		var id string
		if err := pool.QueryRow(ctx, `
			INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
			VALUES (gen_random_uuid(), $1::uuid, 'action', 'failed', '{"instruction":"i"}',
			        jsonb_build_object('task_id', $1::text) || $2::jsonb)
			RETURNING id`, taskID, metadata).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	cases := []struct {
		name, taskID, metadata, wantState string
		wantKeys                          []string
	}{
		{"活任务里被取消的动作", active, `{"blocked_reason":"ReAct 执行失败: LLM 调用失败: context canceled"}`, "open", nil},
		{"活任务里 runner 重启中断的动作", active, `{"failure_reason":"Runner 重启导致执行中断","recovered_at":"2026-11-01T00:00:00Z"}`, "open", nil},
		{"已终止任务里被取消的动作", aborted, `{"blocked_reason":"ReAct 执行失败: context canceled"}`, "aborted", nil},
		{"停摆判死仍是失败", active, `{"failure_reason":"执行超时（420秒无 LLM/工具活动）","recovered_at":"x","previous_state":"running","retryable":true}`, "failed", []string{"failure_reason"}},
		{"业务失败不动", active, `{"blocked_reason":"ReAct 执行失败: 连续 2 次空响应"}`, "failed", []string{"blocked_reason"}},
	}
	ids := make([]string, len(cases))
	for i, c := range cases {
		ids[i] = insert(c.taskID, c.metadata)
	}

	MigrateUp(t, pool)

	for i, c := range cases {
		var state string
		var metadata map[string]any
		if err := pool.QueryRow(ctx, `SELECT state, metadata FROM exploration_node WHERE id = $1`, ids[i]).
			Scan(&state, &metadata); err != nil {
			t.Fatal(err)
		}
		if state != c.wantState {
			t.Errorf("%s：state = %s，期望 %s", c.name, state, c.wantState)
		}
		want := map[string]bool{"task_id": true}
		for _, k := range c.wantKeys {
			want[k] = true
		}
		for k := range metadata {
			if !want[k] {
				t.Errorf("%s：残留键 %s（metadata=%v）", c.name, k, metadata)
			}
		}
		for k := range want {
			if _, ok := metadata[k]; !ok {
				t.Errorf("%s：缺少键 %s", c.name, k)
			}
		}
	}
}
