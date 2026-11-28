//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0265 uint = 265

// 迁移 0265：动作不再带优先级（存量清掉）；此后优先级只属于主张，加急只属于规划者排出的
// 动作、值是 true。
func TestMigration0265_ActionsCarryNoPriority(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0265)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	const task = "33333333-3333-4444-8333-333333333337"
	SeedTask(t, pool, task)
	// suffix → 迁移后是否还应带 priority
	rows := []struct {
		suffix, kind, state, metadata string
		keeps                         bool
	}{
		{"01", "action", "open", `{"source_type":"user","priority":"high"}`, false},                                     // 用户指派的动作
		{"02", "action", "open", `{"source_type":"planner","priority":"critical"}`, false},                              // 规划者排出的动作（0263 之后不该有，防万一）
		{"03", "observation", "", `{"source_type":"executor","priority":"high","verification_status":"pending"}`, true}, // 假设
		{"04", "result", "", `{"source_type":"evaluator","priority":"high"}`, true},                                     // 结果
		{"05", "action", "open", `{"source_type":"planner"}`, false},                                                    // 本来就没有
	}
	idOf := func(suffix string) string { return "33333333-0000-4000-8000-0000000000" + suffix }
	for _, r := range rows {
		var state any
		if r.state != "" {
			state = r.state
		}
		exec(`INSERT INTO exploration_node (id, task_id, kind, state, content, metadata) VALUES ($1, $2, $3, $4, '{}', $5::jsonb)`,
			idOf(r.suffix), task, r.kind, state, r.metadata)
	}

	MigrateTo(t, pool, migration0265)

	for _, r := range rows {
		var has bool
		if err := pool.QueryRow(ctx, `SELECT metadata ? 'priority' FROM exploration_node WHERE id=$1`, idOf(r.suffix)).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has != r.keeps {
			t.Errorf("%s %s（%s）：迁移后带 priority = %v，应为 %v", r.kind, r.suffix, r.metadata, has, r.keeps)
		}
	}

	for name, sql := range map[string]string{
		"动作带优先级":      `UPDATE exploration_node SET metadata = metadata || '{"priority":"high"}' WHERE id='` + idOf("05") + `'`,
		"用户指派的动作带加急":  `UPDATE exploration_node SET metadata = metadata || '{"urgent":true}' WHERE id='` + idOf("01") + `'`,
		"观察带加急":       `UPDATE exploration_node SET metadata = metadata || '{"urgent":true}' WHERE id='` + idOf("03") + `'`,
		"加急的值不是 true": `UPDATE exploration_node SET metadata = metadata || '{"urgent":"yes"}' WHERE id='` + idOf("05") + `'`,
	} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Errorf("迁移后不应接受：%s", name)
		}
	}
	// 规划者排出的动作带加急照常可写
	exec(`UPDATE exploration_node SET metadata = metadata || '{"urgent":true}' WHERE id=$1`, idOf("05"))
	// 目标确实不能带优先级（上面那宗在动作行上验的是动作）
	exec(`INSERT INTO exploration_node (id, task_id, kind, state, content, metadata) VALUES ('`+idOf("06")+`', $1, 'objective', 'open', '{}', '{"source_type":"user"}')`, task)
	if _, err := pool.Exec(ctx, `UPDATE exploration_node SET metadata = metadata || '{"priority":"high"}' WHERE id='`+idOf("06")+`'`); err == nil {
		t.Errorf("迁移后不应接受：目标带优先级")
	}
}
