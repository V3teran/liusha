//go:build integration

package engagement

import (
	"context"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestStore_LookupOrCreate_LazyAndIdempotent 验证：
// 首次调用插入新 engagement；同 (tenant, host) + active 的二次调用幂等返回同一行。
func TestStore_LookupOrCreate_LazyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	got, err := s.LookupOrCreate(ctx, "default", "vulnapp", ModeProxy)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.Status != StatusActive {
		t.Fatalf("unexpected: %+v", got)
	}
	got2, err := s.LookupOrCreate(ctx, "default", "vulnapp", ModeProxy)
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

	e, err := s.LookupOrCreate(ctx, "default", "h", ModeProxy)
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
	e2, err := s.LookupOrCreate(ctx, "default", "h", ModeProxy)
	if err != nil || e2.ID == e.ID {
		t.Fatalf("expected new active, got %+v err=%v", e2, err)
	}
}

// TestStore_AppendNoteAndReadNotes 验证 notes 单层 Append + ReadNotes：
// 写 3 条 note，ReadNotes 返回 JSON 含 notes key 且 3 条全在。
func TestStore_AppendNoteAndReadNotes(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	e, err := s.LookupOrCreate(ctx, "default", "h", ModeProxy)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.AppendNote(ctx, e.ID,
		[]byte(`{"content":"endpoint X 401","agent_run_id":"t1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendNote(ctx, e.ID,
		[]byte(`{"content":"all_differ at threshold 0.3","agent_run_id":"t1"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendNote(ctx, e.ID,
		[]byte(`{"content":"GET /admin","agent_run_id":"t2"}`)); err != nil {
		t.Fatal(err)
	}

	state, err := s.ReadNotes(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := string(state)
	if !strings.Contains(got, `"notes"`) ||
		!strings.Contains(got, "endpoint X 401") ||
		!strings.Contains(got, "all_differ") ||
		!strings.Contains(got, "GET /admin") {
		t.Fatalf("state missing notes/contents: %s", got)
	}
}
