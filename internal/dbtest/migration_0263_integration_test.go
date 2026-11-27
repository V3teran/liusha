//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0263 uint = 263

// 迁移 0263：规划者排出的动作、方向与根目标上的 priority 清掉；假设、结果与用户指派的动作上的
// 优先级有含义（严重度、派发先后），原样保留。
func TestMigration0263_PriorityOnlyWhereItMeansSomething(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0263)
	const task = "33333333-3333-4444-8333-333333333335"
	SeedTask(t, pool, task)
	// suffix 是节点 ID 的末两位；keeps 是迁移后是否还应带 priority
	rows := []struct {
		suffix, kind, state, metadata string
		keeps                         bool
	}{
		{"01", "objective", "open", `{"source_type":"user","priority":"high"}`, false},        // 根目标
		{"02", "objective", "open", `{"source_type":"planner","priority":"critical"}`, false}, // 方向
		{"03", "action", "open", `{"source_type":"planner","priority":"high"}`, false},        // 规划者排出的动作
		{"04", "action", "open", `{"source_type":"user","priority":"high"}`, true},            // 用户指派的动作
		{"05", "observation", "", `{"source_type":"executor","priority":"critical"}`, true},   // 假设
		{"06", "result", "", `{"source_type":"evaluator","priority":"high"}`, true},           // 结果
		{"07", "action", "completed", `{"source_type":"planner"}`, false},                     // 本来就没有
	}
	idOf := func(suffix string) string { return "33333333-0000-4000-8000-0000000000" + suffix }
	for _, r := range rows {
		var state any // 观察与结果没有状态列
		if r.state != "" {
			state = r.state
		}
		if _, err := pool.Exec(ctx, `INSERT INTO exploration_node (id, task_id, kind, state, content, metadata)
			VALUES ($1, $2, $3, $4, '{}', $5::jsonb)`, idOf(r.suffix), task, r.kind, state, r.metadata); err != nil {
			t.Fatalf("插入 %s: %v", r.suffix, err)
		}
	}

	MigrateTo(t, pool, migration0263)

	for _, r := range rows {
		var has bool
		if err := pool.QueryRow(ctx, `SELECT metadata ? 'priority' FROM exploration_node WHERE id=$1`, idOf(r.suffix)).Scan(&has); err != nil {
			t.Fatal(err)
		}
		if has != r.keeps {
			t.Errorf("%s %s（%s）：迁移后带 priority = %v，应为 %v", r.kind, r.suffix, r.metadata, has, r.keeps)
		}
		var source string
		if err := pool.QueryRow(ctx, `SELECT metadata->>'source_type' FROM exploration_node WHERE id=$1`, idOf(r.suffix)).Scan(&source); err != nil {
			t.Fatal(err)
		}
		if source == "" {
			t.Errorf("%s：清 priority 不应动其余元数据", r.suffix)
		}
	}
}
