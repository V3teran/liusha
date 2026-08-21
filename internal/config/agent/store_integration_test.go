//go:build integration

package operator

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestStore_CreateThenGetByCode 验证：建操作员后按 code 回读一致，tools jsonb 往返正确。
func TestStore_CreateThenGetByCode(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	created, err := s.Create(ctx, NewParams{
		Code:          "recon",
		Kind:          KindExecutor,
		Name:          "侦察操作员",
		Description:   "资产测绘与信息收集",
		Body:          "# 方法论\n先枚举再指纹",
		FunctionTools: []string{"run_command", "http_request"},
		MaxIterations: 30,
		Enabled:       true,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if created.ID == "" {
		t.Fatal("create 应回填 uuid")
	}

	got, err := s.GetByCode(ctx, "recon")
	if err != nil {
		t.Fatalf("get by code: %v", err)
	}
	if got.Name != "侦察操作员" || got.Kind != KindExecutor {
		t.Fatalf("字段不匹配: %+v", got)
	}
	if len(got.FunctionTools) != 2 || got.FunctionTools[0] != "run_command" || got.FunctionTools[1] != "http_request" {
		t.Fatalf("function_tools jsonb 往返错误: %+v", got.FunctionTools)
	}
	if got.MaxIterations != 30 {
		t.Fatalf("max_iterations=%d, want 30", got.MaxIterations)
	}
}

// TestStore_CreateRejectsBadKind 验证：非法 kind 在应用层被拒（不落库）。
func TestStore_CreateRejectsBadKind(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	if _, err := s.Create(ctx, NewParams{Code: "x", Kind: "bogus", Name: "n"}); err == nil {
		t.Fatal("非法 kind 应报错")
	}
}

// TestStore_ListOnlyEnabled 验证：List(onlyEnabled=true) 过滤 enabled=false。
func TestStore_ListOnlyEnabled(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	if _, err := s.Create(ctx, NewParams{Code: "on", Kind: KindExecutor, Name: "on", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Create(ctx, NewParams{Code: "off", Kind: KindExecutor, Name: "off", Enabled: false}); err != nil {
		t.Fatal(err)
	}

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

// TestStore_GetOrchestrator 验证：全局唯一编排操作员可取；零条/多条均报错。
func TestStore_GetOrchestrator(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	// 零条 → 报错
	if _, err := s.GetOrchestrator(ctx); err == nil {
		t.Fatal("无编排操作员时应报错")
	}

	if _, err := s.Create(ctx, NewParams{Code: "orch", Kind: KindPlanner, Name: "编排", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetOrchestrator(ctx)
	if err != nil {
		t.Fatalf("get orchestrator: %v", err)
	}
	if got.Code != "orch" {
		t.Fatalf("orchestrator code=%q, want orch", got.Code)
	}

	// 第二条 enabled orchestrator → 违反全局唯一 → 报错
	if _, err := s.Create(ctx, NewParams{Code: "orch2", Kind: KindPlanner, Name: "编排2", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetOrchestrator(ctx); err == nil {
		t.Fatal("多条编排操作员应报错")
	}
}

// TestStore_UpdateAndDelete 验证：Update 改字段 + bump updated_at；Delete 生效。
func TestStore_UpdateAndDelete(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	created, err := s.Create(ctx, NewParams{Code: "c", Kind: KindExecutor, Name: "旧名", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := s.Update(ctx, NewParams{Code: "c", Kind: KindExecutor, Name: "新名", Enabled: false})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "新名" || updated.Enabled {
		t.Fatalf("update 未生效: %+v", updated)
	}
	if !updated.UpdatedAt.After(created.UpdatedAt) {
		t.Fatalf("updated_at 应被 bump: %v vs %v", updated.UpdatedAt, created.UpdatedAt)
	}
	if err := s.Delete(ctx, "c"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetByCode(ctx, "c"); err == nil {
		t.Fatal("删后 GetByCode 应报错")
	}
}
