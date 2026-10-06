//go:build integration

package agent

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/jackc/pgx/v5/pgxpool"
)

// insertAgent 直接 SQL 插入 agent 行。生产路径经 config/seed 从 agents/*.md 导入，
// Store 不暴露 Create；测试用 SQL 构造夹具。
func insertAgent(t *testing.T, pool *pgxpool.Pool, code string, enabled bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO agent (code, name, description, system_prompt, function_tools, cli_tools, skills, max_iterations, complexity, enabled)
		 VALUES ($1, $2, '', '# prompt', '["run_command","http_request"]'::jsonb, '[]'::jsonb, '[]'::jsonb, 30, 'medium', $3)`,
		code, code, enabled)
	if err != nil {
		t.Fatalf("insert agent %s: %v", code, err)
	}
}

// TestStore_GetByCodeRoundTrip 验证：GetByCode 回读一致，function_tools jsonb 往返正确。
func TestStore_GetByCodeRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	insertAgent(t, pool, "recon", true)

	got, err := s.GetByCode(ctx, "recon")
	if err != nil {
		t.Fatalf("get by code: %v", err)
	}
	if got.Name != "recon" {
		t.Fatalf("字段不匹配: %+v", got)
	}
	if got.SystemPrompt != "# prompt" {
		t.Fatalf("system_prompt 往返错误: %q", got.SystemPrompt)
	}
	if len(got.FunctionTools) != 2 || got.FunctionTools[0] != "run_command" || got.FunctionTools[1] != "http_request" {
		t.Fatalf("function_tools jsonb 往返错误: %+v", got.FunctionTools)
	}
	if got.MaxIterations != 30 {
		t.Fatalf("max_iterations=%d, want 30", got.MaxIterations)
	}
}

// TestStore_ListOnlyEnabled 验证：List(onlyEnabled=true) 过滤 enabled=false。
func TestStore_ListOnlyEnabled(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	insertAgent(t, pool, "on", true)
	insertAgent(t, pool, "off", false)

	all, err := s.List(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("List(false) 应返回 2 条，得 %d", len(all))
	}
	enabled, err := s.List(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 1 || enabled[0].Code != "on" {
		t.Fatalf("List(true) 应只返回 enabled，得 %+v", enabled)
	}
}

// TestStore_Update 验证：Update 修改 system_prompt/function_tools 并 bump updated_at。
func TestStore_Update(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	insertAgent(t, pool, "c", true)
	before, err := s.GetByCode(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}

	newPrompt := "# new prompt"
	newTools := []string{"drive_browser"}
	updated, err := s.Update(ctx, "c", UpdateParams{
		SystemPrompt:  &newPrompt,
		FunctionTools: &newTools,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.SystemPrompt != newPrompt {
		t.Fatalf("system_prompt 未更新: %q", updated.SystemPrompt)
	}
	if len(updated.FunctionTools) != 1 || updated.FunctionTools[0] != "drive_browser" {
		t.Fatalf("function_tools 未更新: %+v", updated.FunctionTools)
	}
	if !updated.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at 应被 bump: %v vs %v", updated.UpdatedAt, before.UpdatedAt)
	}
}
