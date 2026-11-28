//go:build integration

package dbtest

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/llmconfig"
)

const migration0266 uint = 266

// 迁移 0266：上下文窗口与单次最大输出的缺省取当前旗舰模型的官方上限（1M / 128K）。库列缺省与
// 代码里唯一的出处同值（全局一致由这里守住）；存量为 0（未设置）的行落成缺省，已填的不动。
func TestMigration0266_LimitDefaults(t *testing.T) {
	ctx := context.Background()
	pool := NewPgPoolBefore(t, migration0266)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	limits := func(code string) (maxTokens, contextWindow int) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT max_tokens, context_window FROM llm_provider WHERE code=$1`, code).Scan(&maxTokens, &contextWindow); err != nil {
			t.Fatal(err)
		}
		return maxTokens, contextWindow
	}
	const insert = `INSERT INTO llm_provider (code, type, base_url, default_model, context_window, display_name, auth_scheme, max_tokens, supports_vision) VALUES ($1, 'anthropic', 'https://x', 'm', $2, $3, 'bearer', $4, false)`
	exec(insert, "00000000-0000-4000-8000-000000000001", 65536, "未设置", 0)
	exec(insert, "00000000-0000-4000-8000-000000000002", 200000, "已填", 8192)

	MigrateTo(t, pool, migration0266)

	// 库列缺省与代码常量同值
	exec(`INSERT INTO llm_provider (code, type, base_url, default_model, display_name, auth_scheme, supports_vision) VALUES ('00000000-0000-4000-8000-000000000003', 'anthropic', 'https://x', 'm', '缺省', 'bearer', false)`)
	if maxTokens, contextWindow := limits("00000000-0000-4000-8000-000000000003"); maxTokens != llmconfig.DefaultMaxTokens || contextWindow != llmconfig.DefaultContextWindow {
		t.Fatalf("库列缺省应与代码常量同值（%d / %d），实际 %d / %d", llmconfig.DefaultMaxTokens, llmconfig.DefaultContextWindow, maxTokens, contextWindow)
	}
	// 存量：未设置的落成缺省；已填的不动
	if maxTokens, _ := limits("00000000-0000-4000-8000-000000000001"); maxTokens != llmconfig.DefaultMaxTokens {
		t.Errorf("未设置的 max_tokens 应落成缺省 %d，实际 %d", llmconfig.DefaultMaxTokens, maxTokens)
	}
	if maxTokens, contextWindow := limits("00000000-0000-4000-8000-000000000002"); maxTokens != 8192 || contextWindow != 200000 {
		t.Errorf("已填的值不应被改动，实际 %d / %d", maxTokens, contextWindow)
	}
}
