//go:build integration

package activescan

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestStore_CreateThenComplete 验证：active → completed 自然完成终态，
// 写 ended_at 且不带 error_message。隐含校验 migration 0063 的 CHECK 约束接受 'completed'
// （约束未放开时 Complete 会因 check violation 报错）。
func TestStore_CreateThenComplete(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	sc, err := s.Create(ctx, "测试 brief：扫 http://example.test 找 XSS", "example.test:8080")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if sc.Status != StatusActive {
		t.Fatalf("初始 status 应为 active, got %s", sc.Status)
	}
	if sc.EndedAt != nil {
		t.Fatalf("active scan 不应有 ended_at")
	}

	if err := s.Complete(ctx, sc.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	got, err := s.GetByID(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusCompleted {
		t.Fatalf("status=%s, want completed", got.Status)
	}
	if got.EndedAt == nil {
		t.Fatalf("completed scan 应写 ended_at")
	}
	if got.ErrorMessage != "" {
		t.Fatalf("completed 不应有 error_message, got %q", got.ErrorMessage)
	}
}

// TestStore_Abort 验证：aborted 终态写 error_message（区别于 completed 的无错误收尾）。
func TestStore_Abort(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	sc, err := s.Create(ctx, "测试 brief", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Abort(ctx, sc.ID, "user stop"); err != nil {
		t.Fatalf("abort: %v", err)
	}

	got, err := s.GetByID(ctx, sc.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != StatusAborted {
		t.Fatalf("status=%s, want aborted", got.Status)
	}
	if got.ErrorMessage != "user stop" {
		t.Fatalf("error_message=%q, want 'user stop'", got.ErrorMessage)
	}
}
