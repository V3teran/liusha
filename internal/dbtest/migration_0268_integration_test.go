//go:build integration

package dbtest

import (
	"context"
	"testing"

	fwllm "github.com/V3teran/liusha/internal/framework/llm"
)

const migration0268 uint = 268

// 迁移 0268：扩展思考预算（按 token）换成思考深度（档位）。旧列删除、新列缺省「没设置」，取值
// 受约束；库列缺省与约束里的取值同 framework/llm 的档位表对照。
func TestMigration0268_ReasoningEffort(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0268)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	query := func(sql string, args ...any) (int, string) {
		t.Helper()
		var n int
		var s string
		if err := pool.QueryRow(ctx, sql, args...).Scan(&n, &s); err != nil {
			t.Fatal(err)
		}
		return n, s
	}
	const insert = `INSERT INTO llm_provider (code, type, base_url, default_model, context_window, display_name, auth_scheme, supports_vision, thinking_budget_tokens) VALUES ($1, 'anthropic_messages', 'https://x', 'm', 1048576, $2, 'bearer', false, $3)`
	exec(insert, "00000000-0000-4000-8000-000000000021", "有预算", 4096)
	exec(insert, "00000000-0000-4000-8000-000000000022", "无预算", 0)

	MigrateTo(t, pool, migration0268)

	// 旧预算换算不成档位：存量一律「没设置」
	for _, code := range []string{"00000000-0000-4000-8000-000000000021", "00000000-0000-4000-8000-000000000022"} {
		var effort string
		if err := pool.QueryRow(ctx, `SELECT reasoning_effort FROM llm_provider WHERE code=$1`, code).Scan(&effort); err != nil {
			t.Fatal(err)
		}
		if effort != "" {
			t.Errorf("%s 迁移后思考深度应为没设置，实际 %q", code, effort)
		}
	}
	// 旧列已删
	if n, _ := query(`SELECT count(*), '' FROM information_schema.columns WHERE table_name='llm_provider' AND column_name='thinking_budget_tokens'`); n != 0 {
		t.Error("旧列 thinking_budget_tokens 应已删除")
	}
	if n, _ := query(`SELECT count(*), '' FROM information_schema.columns WHERE table_name='llm_provider' AND column_name='reasoning_effort'`); n != 1 {
		t.Error("新列 reasoning_effort 应存在")
	}
	exec(`INSERT INTO llm_provider (code, type, base_url, default_model, display_name, auth_scheme, supports_vision, reasoning_effort) VALUES ('00000000-0000-4000-8000-000000000023', 'anthropic_messages', 'https://x', 'm', '各档', 'bearer', false, 'max')`)
	if _, effort := query(`SELECT 0, reasoning_effort FROM llm_provider WHERE code='00000000-0000-4000-8000-000000000023'`); effort != "max" {
		t.Errorf("思考深度应可写 max，实际 %q", effort)
	}
	// 不带该列的行落「没设置」
	exec(`INSERT INTO llm_provider (code, type, base_url, default_model, display_name, auth_scheme, supports_vision) VALUES ('00000000-0000-4000-8000-000000000024', 'anthropic_messages', 'https://x', 'm', '缺省', 'bearer', false)`)
	if _, effort := query(`SELECT 0, reasoning_effort FROM llm_provider WHERE code='00000000-0000-4000-8000-000000000024'`); effort != "" {
		t.Errorf("没给思考深度应为没设置，实际 %q", effort)
	}
	// 约束与档位表同口径：表里的每个档位都可写，「无」与别的写法被拒
	for _, effort := range fwllm.ReasoningEffortNames() {
		exec(`UPDATE llm_provider SET reasoning_effort=$1 WHERE code='00000000-0000-4000-8000-000000000023'`, effort)
	}
	for _, bad := range []string{"none", "xhigh", "LOW"} {
		if _, err := pool.Exec(ctx, `UPDATE llm_provider SET reasoning_effort=$1 WHERE code='00000000-0000-4000-8000-000000000023'`, bad); err == nil {
			t.Errorf("迁移后不应接受思考深度 %q", bad)
		}
	}
}
