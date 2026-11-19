//go:build integration

package dbtest

import (
	"context"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/builtinsync"
)

const migration0235 uint = 235

// 迁移 0235：技能从分层结构（skill_override + custom_skill）搬进单表 skill。
//   - 自建技能整行搬入；
//   - 自定义过的内置技能取 last_saved：只改过启停的，seed_hash = 当时内容的哈希（内容未改，出厂
//     同步时跟随新版）；改过内容的 seed_hash 为 NULL（基线未知，视为已改）；
//   - 与自建同 code 的内置自定义丢弃（原本就被遮蔽）。
//
// 迁移 SQL 的哈希与 builtinsync.Hash 必须同一算式，否则只停用过的技能会被误判为改过。
// 迁移前的定时模板沿用渗透测试场景，存量任务写入渗透测试场景的快照（它们原本就按渗透测试运行）。
func TestMigration0235_SkillSingleTableAndScenario(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0235)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO custom_skill (code, category, name, description, body, enabled) VALUES
		('mine', 'vuln', '我的', 'd', '自建正文', true),
		('bac', 'vuln', '我的 BAC', 'd', '遮蔽内置', true)`)
	saved := func(name, body string, enabled bool) string {
		return `{"code":"x","category":"tool","name":"` + name + `","description":"描述","body":"` + body + `","enabled":` +
			map[bool]string{true: "true", false: "false"}[enabled] + `,"is_builtin":true}`
	}
	exec(`INSERT INTO skill_override (skill_code, fields, last_saved) VALUES
		('browser-use', '{"enabled":{"value":false}}', $1::jsonb),
		('edited', '{"body":{"value":"改过"}}', $2::jsonb),
		('bac', '{"body":{"value":"被遮蔽"}}', $3::jsonb)`,
		saved("浏览器", "当年出厂正文", false), saved("改过的", "改过", true), saved("BAC", "被遮蔽", true))
	exec(`INSERT INTO cron_schedule (cron_expr, payload, title, next_run_at) VALUES ('0 3 * * *', '[]', 't', now())`)
	assignment := SeedAssignment(t, pool)
	exec(`INSERT INTO task (assignment_id, goal) VALUES ($1, '存量任务')`, assignment)

	MigrateTo(t, pool, migration0235)

	type row struct {
		category, name, body string
		enabled, builtin     bool
		seedHash             *string
	}
	get := func(code string) (row, bool) {
		var r row
		err := pool.QueryRow(ctx, `SELECT category, name, body, enabled, is_builtin, seed_hash FROM skill WHERE code=$1`, code).
			Scan(&r.category, &r.name, &r.body, &r.enabled, &r.builtin, &r.seedHash)
		return r, err == nil
	}
	if r, ok := get("mine"); !ok || r.builtin || r.body != "自建正文" || r.seedHash != nil {
		t.Errorf("自建技能应整行搬入: %+v %v", r, ok)
	}
	if r, ok := get("bac"); !ok || r.builtin || r.name != "我的 BAC" {
		t.Errorf("与自建同 code 的内置自定义应丢弃、自建保留: %+v %v", r, ok)
	}
	r, ok := get("browser-use")
	if !ok || !r.builtin || r.enabled || r.category != "tool" || r.seedHash == nil ||
		*r.seedHash != builtinsync.Hash("浏览器", "描述", "当年出厂正文") {
		t.Errorf("只停用过的内置技能：内容为当时出厂、基线为其哈希（与 builtinsync.Hash 同算式）: %+v %v", r, ok)
	}
	if r, ok := get("edited"); !ok || !r.builtin || r.body != "改过" || r.seedHash != nil {
		t.Errorf("改过内容的内置技能基线未知: %+v %v", r, ok)
	}
	for _, old := range []string{"skill_override", "custom_skill"} {
		var exists bool
		_ = pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+old).Scan(&exists)
		if exists {
			t.Errorf("%s 应已删除", old)
		}
	}
	var scenario string
	if err := pool.QueryRow(ctx, `SELECT scenario FROM cron_schedule`).Scan(&scenario); err != nil || scenario != "pentest" {
		t.Errorf("已有定时模板应沿用渗透测试场景: %q %v", scenario, err)
	}
	var code, name, body string
	if err := pool.QueryRow(ctx, `SELECT scenario, scenario_name, scenario_body FROM task WHERE goal='存量任务'`).Scan(&code, &name, &body); err != nil ||
		code != "pentest" || name != "渗透测试" || !strings.Contains(body, "可交付的主张") {
		t.Errorf("存量任务应写入渗透测试场景快照: %q %q %d字 %v", code, name, len([]rune(body)), err)
	}
	if _, err := pool.Exec(ctx, `INSERT INTO task (assignment_id, goal, scenario) VALUES ($1, 'x', 'pentest')`, assignment); err == nil {
		t.Error("场景快照三列须同空同有")
	}
}

// 0235 回退：自建技能回 custom_skill；改过、停用或删除过的内置技能回 skill_override（旧结构没有
// 删除，删除过的按停用保留）；没改过的内置技能不需要行；场景相关的表与列全部撤掉。
func TestMigration0235_Down(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0235)
	MigrateTo(t, pool, migration0235)
	h := builtinsync.Hash("浏览器", "", "出厂正文")
	for _, sql := range []string{
		`INSERT INTO skill (code, category, name, body) VALUES ('mine', 'vuln', '我的', '自建正文')`,
		`INSERT INTO skill (code, category, name, body, is_builtin, seed_hash) VALUES ('clean', 'tool', '浏览器', '出厂正文', true, '` + h + `')`,
		`INSERT INTO skill (code, category, name, body, is_builtin, seed_hash, deleted_at) VALUES ('gone', 'tool', '浏览器', '出厂正文', true, '` + h + `', now())`,
	} {
		if _, err := pool.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	MigrateTo(t, pool, migration0235-1)

	var custom string
	if err := pool.QueryRow(ctx, `SELECT code FROM custom_skill`).Scan(&custom); err != nil || custom != "mine" {
		t.Errorf("自建技能应回 custom_skill: %q %v", custom, err)
	}
	rows, err := pool.Query(ctx, `SELECT skill_code, fields->'enabled'->>'value' FROM skill_override ORDER BY skill_code`)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for rows.Next() {
		var code, enabled string
		_ = rows.Scan(&code, &enabled)
		got = append(got, code+"="+enabled)
	}
	rows.Close()
	if len(got) != 1 || got[0] != "gone=false" {
		t.Errorf("只有删除过的内置技能回用户层且按停用保留: %v", got)
	}
	for _, rel := range []string{"skill", "scenario"} {
		var exists bool
		_ = pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+rel).Scan(&exists)
		if exists {
			t.Errorf("%s 应已删除", rel)
		}
	}
	var cols int
	_ = pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name IN ('task','cron_schedule') AND column_name LIKE 'scenario%'`).Scan(&cols)
	if cols != 0 {
		t.Errorf("场景相关列应已撤掉，剩 %d 列", cols)
	}
}
