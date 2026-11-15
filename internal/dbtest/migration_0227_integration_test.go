//go:build integration

package dbtest

import (
	"context"
	"strings"
	"testing"
)

const migration0227 uint = 227

// 迁移 0227：批次与定时模板的名称在写入时确定。存量里没写名称的，按下发时的规则补齐——
// 首个目标的前 40 个字（代理捕获批次取主机）；之后名称不得为空。
func TestMigration0227_BatchTitle(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0227)
	long := strings.Repeat("长", 45)
	insertAssignment := func(source, payload, title string) string {
		var id string
		if err := pool.QueryRow(ctx, `INSERT INTO assignment (source, payload, title) VALUES ($1, $2::jsonb, $3) RETURNING id`,
			source, payload, title).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	untitled := insertAssignment("direct", `[{"brief":"扫描 http://a.test"},{"brief":"扫描 http://b.test"}]`, "")
	untitledLong := insertAssignment("direct", `[{"brief":"`+long+`"}]`, "")
	proxyUntitled := insertAssignment("proxy", `[{"host":"c.test"}]`, "")
	titled := insertAssignment("direct", `[{"brief":"x"}]`, "夜间复扫")
	var schedule string
	if err := pool.QueryRow(ctx, `INSERT INTO cron_schedule (cron_expr, payload, title) VALUES ('0 4 * * *', '[{"brief":"每日巡检 http://d.test"}]', '') RETURNING id`).
		Scan(&schedule); err != nil {
		t.Fatal(err)
	}

	MigrateTo(t, pool, migration0227)

	title := func(table, id string) string {
		var s string
		if err := pool.QueryRow(ctx, `SELECT title FROM `+table+` WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	for _, c := range []struct{ name, table, id, want string }{
		{"未命名批次取首个目标", "assignment", untitled, "扫描 http://a.test"},
		{"首个目标截取前 40 个字", "assignment", untitledLong, strings.Repeat("长", 40)},
		{"代理捕获批次取主机", "assignment", proxyUntitled, "c.test"},
		{"已命名的不动", "assignment", titled, "夜间复扫"},
		{"未命名模板取首个目标", "cron_schedule", schedule, "每日巡检 http://d.test"},
	} {
		if got := title(c.table, c.id); got != c.want {
			t.Errorf("%s：得 %q，期望 %q", c.name, got, c.want)
		}
	}
	if _, err := pool.Exec(ctx, `INSERT INTO assignment (source, payload) VALUES ('direct', '[]')`); err == nil {
		t.Error("批次名为空应被拒绝")
	}
	if _, err := pool.Exec(ctx, `UPDATE cron_schedule SET title = '' WHERE id = $1`, schedule); err == nil {
		t.Error("模板名为空应被拒绝")
	}
}
