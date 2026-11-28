//go:build integration

package dbtest

import (
	"context"
	"testing"
)

const migration0267 uint = 267

// 迁移 0267：协议取值改为「厂商 + 接口名」，存量按原名改写；之后旧取值被约束拒绝。
func TestMigration0267_ProtocolNames(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0267)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	const insert = `INSERT INTO llm_provider (code, type, base_url, default_model, context_window, display_name, auth_scheme, supports_vision) VALUES ($1, $2, 'https://x', 'm', 1048576, $3, 'bearer', false)`
	exec(insert, "00000000-0000-4000-8000-000000000011", "openai_compat", "甲")
	exec(insert, "00000000-0000-4000-8000-000000000012", "anthropic", "乙")

	MigrateTo(t, pool, migration0267)

	rows, err := pool.Query(ctx, `SELECT code, type FROM llm_provider ORDER BY code`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]string{
		"00000000-0000-4000-8000-000000000011": "openai_chat_completions",
		"00000000-0000-4000-8000-000000000012": "anthropic_messages",
	}
	n := 0
	for rows.Next() {
		var code, typ string
		if err := rows.Scan(&code, &typ); err != nil {
			t.Fatal(err)
		}
		if want[code] != typ {
			t.Errorf("%s 迁移后取值 %q，应为 %q", code, typ, want[code])
		}
		n++
	}
	if n != len(want) {
		t.Fatalf("应有 %d 行，实际 %d", len(want), n)
	}
	for _, old := range []string{"openai_compat", "anthropic"} {
		if _, err := pool.Exec(ctx, insert, "00000000-0000-4000-8000-000000000013", old, "丙"); err == nil {
			t.Errorf("迁移后不应再接受旧取值 %q", old)
		}
	}
	exec(insert, "00000000-0000-4000-8000-000000000014", "anthropic_messages", "丁")
}
