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
func insertAgent(t *testing.T, pool *pgxpool.Pool, code string, kind Kind, enabled bool) {
	t.Helper()
	_, err := pool.Exec(context.Background(),
		`INSERT INTO agent (code, kind, name, description, system_prompt, function_tools, cli_tools, skills, max_iterations, complexity, enabled)
		 VALUES ($1, $2, $3, '', '# prompt', '["run_command","http_request"]'::jsonb, '[]'::jsonb, '[]'::jsonb, 30, 'medium', $4)`,
		code, string(kind), code, enabled)
	if err != nil {
		t.Fatalf("insert agent %s: %v", code, err)
	}
}

// TestStore_GetByCodeRoundTrip 验证：GetByCode 回读一致，function_tools jsonb 往返正确。
func TestStore_GetByCodeRoundTrip(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	insertAgent(t, pool, "recon", KindExecutor, true)

	got, err := s.GetByCode(ctx, "recon")
	if err != nil {
		t.Fatalf("get by code: %v", err)
	}
	if got.Name != "recon" || got.Kind != KindExecutor {
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

	insertAgent(t, pool, "on", KindExecutor, true)
	insertAgent(t, pool, "off", KindExecutor, false)

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

// TestStore_GetPlanner 验证：唯一 enabled planner 可取；零条报错；第二条被唯一索引拒绝。
func TestStore_GetPlanner(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	// 零条 → 报错
	if _, err := s.GetPlanner(ctx); err == nil {
		t.Fatal("无编排操作员时应报错")
	}

	insertAgent(t, pool, "orch", KindPlanner, true)
	got, err := s.GetPlanner(ctx)
	if err != nil {
		t.Fatalf("get planner: %v", err)
	}
	if got.Code != "orch" {
		t.Fatalf("planner code=%q, want orch", got.Code)
	}

	// 第二条 enabled planner 违反 idx_agent_kind_enabled 唯一索引 → 插入被拒，
	// 数据层兜底"每 kind 至多一个 enabled"的约束。
	if _, err := pool.Exec(ctx,
		`INSERT INTO agent (code, kind, name, system_prompt) VALUES ('orch2', 'planner', 'p2', '')`); err == nil {
		t.Fatal("第二条 enabled planner 应被唯一索引拒绝")
	}
	if _, err := s.GetPlanner(ctx); err != nil {
		t.Fatalf("唯一 planner 不应报错: %v", err)
	}
}

// TestStore_Update 验证：Update 修改 system_prompt/function_tools 并 bump updated_at。
func TestStore_Update(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	insertAgent(t, pool, "c", KindExecutor, true)
	before, err := s.GetByCode(ctx, "c")
	if err != nil {
		t.Fatal(err)
	}

	newPrompt := "# new prompt"
	newTools := []string{"browser_use"}
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
	if len(updated.FunctionTools) != 1 || updated.FunctionTools[0] != "browser_use" {
		t.Fatalf("function_tools 未更新: %+v", updated.FunctionTools)
	}
	if !updated.UpdatedAt.After(before.UpdatedAt) {
		t.Fatalf("updated_at 应被 bump: %v vs %v", updated.UpdatedAt, before.UpdatedAt)
	}
}
