//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0225 uint = 225

// 迁移 0225：任务目标只有一处事实源。task.brief 改名 goal，取值对齐智能体实际看到的
// 根目标描述（下发时拼上的附件清单、代理捕获的完整说明）；没有根目标的任务补建一个，
// 状态跟随任务；run 不再携带输入，agent_run.input 删除。
func TestMigration0225_TaskGoal(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0225)
	assignment := SeedAssignment(t, pool)
	newTask := func(brief, status string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO task (assignment_id, brief, status) VALUES ($1, $2, $3) RETURNING id`,
			assignment, brief, status).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	withRoot := newTask("测试登录页", "active")
	if _, err := pool.Exec(ctx, `
		INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
		VALUES (gen_random_uuid(), $1::uuid, 'objective', 'open',
		        jsonb_build_object('description', '测试登录页' || chr(10) || '附件：inbox/a.txt'), '{"source_type":"user"}')`,
		withRoot); err != nil {
		t.Fatal(err)
	}
	activeBare := newTask("扫描 http://a.test", "active")
	completedBare := newTask("扫描 http://b.test", "completed")
	if _, err := pool.Exec(ctx, `
		INSERT INTO agent_run (task_id, role, input) VALUES ($1::uuid, 'planner', '{"brief":"x"}')`, withRoot); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0225)

	goal := func(id string) string {
		var g string
		if err := pool.QueryRow(ctx, `SELECT goal FROM task WHERE id = $1`, id).Scan(&g); err != nil {
			t.Fatal(err)
		}
		return g
	}
	if g := goal(withRoot); g != "测试登录页\n附件：inbox/a.txt" {
		t.Errorf("目标应对齐根目标描述，得 %q", g)
	}
	root := func(id string) (state, description, source, priority string, n int) {
		if err := pool.QueryRow(ctx, `
			SELECT min(state), min(content->>'description'), min(metadata->>'source_type'), coalesce(min(metadata->>'priority'), ''), count(*)
			  FROM exploration_node WHERE task_id = $1 AND kind = 'objective'`, id).
			Scan(&state, &description, &source, &priority, &n); err != nil {
			t.Fatal(err)
		}
		return
	}
	if st, desc, src, prio, n := root(activeBare); n != 1 || st != "open" || desc != "扫描 http://a.test" || src != "user" || prio != "high" {
		t.Errorf("进行中的任务应补建 open 根目标，得 n=%d %s %q %s %s", n, st, desc, src, prio)
	}
	if st, _, _, _, n := root(completedBare); n != 1 || st != "completed" {
		t.Errorf("已完成的任务补建的根目标应为 completed，得 n=%d %s", n, st)
	}
	if _, _, _, _, n := root(withRoot); n != 1 {
		t.Errorf("已有根目标的任务不应再建，得 %d 个", n)
	}
	var hasInput bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM information_schema.columns
		WHERE table_name = 'agent_run' AND column_name = 'input')`).Scan(&hasInput); err != nil {
		t.Fatal(err)
	}
	if hasInput {
		t.Error("agent_run.input 应已删除")
	}
}
