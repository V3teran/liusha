//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0264 uint = 264

// 迁移 0264：线索从布尔信号 opens_deeper 变成结构化字段 lead。存量的信号转成不带三样的线索，
// 关闭批注原样保留；此后线索只属于事实观察、是个对象，关闭批注只落在线索上。
func TestMigration0264_LeadBecomesAStructuredField(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0264)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	const task = "33333333-3333-4444-8333-333333333336"
	SeedTask(t, pool, task)
	// id 末两位 → 迁移后是不是线索
	rows := []struct {
		suffix, kind, state, metadata string
		isLead                        bool
	}{
		{"01", "observation", "", `{"source_type":"executor","opens_deeper":true}`, true},                                               // 开着的线索
		{"02", "observation", "", `{"source_type":"executor","opens_deeper":true,"lead_closed":{"kind":"blocked","reason":"r"}}`, true}, // 已关闭的线索
		{"03", "observation", "", `{"source_type":"executor","opens_deeper":false}`, false},                                             // 从未有过取 false 的信号，防万一
		{"04", "observation", "", `{"source_type":"executor","verification_status":"pending"}`, false},                                  // 假设
		{"05", "action", "open", `{"source_type":"planner"}`, false},
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

	MigrateTo(t, pool, migration0264)

	for _, r := range rows {
		var hasLead, hasSignal, hasClosure bool
		if err := pool.QueryRow(ctx, `SELECT metadata ? 'lead', metadata ? 'opens_deeper', metadata ? 'lead_closed' FROM exploration_node WHERE id=$1`, idOf(r.suffix)).
			Scan(&hasLead, &hasSignal, &hasClosure); err != nil {
			t.Fatal(err)
		}
		if hasLead != r.isLead || hasSignal {
			t.Errorf("%s（%s）：迁移后 lead=%v opens_deeper=%v，应为 lead=%v、无旧信号", r.suffix, r.metadata, hasLead, hasSignal, r.isLead)
		}
		if want := r.suffix == "02"; hasClosure != want {
			t.Errorf("%s：关闭批注应%s保留，得 %v", r.suffix, map[bool]string{true: "", false: "不"}[want], hasClosure)
		}
	}

	// 约束：线索只属于事实观察、是个对象；关闭批注只落在线索上
	for name, sql := range map[string]string{
		"动作上带线索":       `UPDATE exploration_node SET metadata = metadata || '{"lead":{}}' WHERE id='` + idOf("05") + `'`,
		"假设上带线索":       `UPDATE exploration_node SET metadata = metadata || '{"lead":{}}' WHERE id='` + idOf("04") + `'`,
		"线索不是对象":       `UPDATE exploration_node SET metadata = metadata || '{"lead":true}' WHERE id='` + idOf("01") + `'`,
		"没有线索的观察带关闭批注": `UPDATE exploration_node SET metadata = metadata || '{"lead_closed":{"kind":"blocked"}}' WHERE id='` + idOf("03") + `'`,
	} {
		if _, err := pool.Exec(ctx, sql); err == nil {
			t.Errorf("迁移后不应接受：%s", name)
		}
	}
	// 带三样的线索、线索上的关闭批注照常可写
	exec(`UPDATE exploration_node SET metadata = metadata || '{"lead":{"next":"n","question":"q","potential":"p"}}' WHERE id=$1`, idOf("01"))
	exec(`UPDATE exploration_node SET metadata = metadata || '{"lead_closed":{"kind":"blocked","reason":"受阻"}}' WHERE id=$1`, idOf("01"))
}
