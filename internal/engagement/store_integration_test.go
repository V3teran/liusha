//go:build integration

package engagement

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestStore_LookupOrCreate_LazyAndIdempotent 验证：
// 首次调用插入新 engagement；同 host + active 的二次调用幂等返回同一行。
func TestStore_LookupOrCreate_LazyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	got, err := s.LookupOrCreate(ctx, "vulnapp", ModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.Status != StatusActive {
		t.Fatalf("unexpected: %+v", got)
	}
	got2, err := s.LookupOrCreate(ctx, "vulnapp", ModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != got2.ID {
		t.Fatalf("expected idempotent, got %s vs %s", got.ID, got2.ID)
	}
}

// TestStore_Abort 验证：Abort 后 status=aborted；新调用 LookupOrCreate 会插入新 active 行。
func TestStore_Abort(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	e, err := s.LookupOrCreate(ctx, "h", ModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Abort(ctx, e.ID, ""); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetByID(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != StatusAborted {
		t.Fatalf("status: %s", got.Status)
	}
	e2, err := s.LookupOrCreate(ctx, "h", ModeProxy)
	if err != nil || e2.ID == e.ID {
		t.Fatalf("expected new active, got %+v err=%v", e2, err)
	}
}
