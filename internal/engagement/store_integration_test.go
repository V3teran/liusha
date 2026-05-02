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
	if err := s.Abort(ctx, e.ID); err != nil {
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

// TestStore_AppendAndReadState 验证三层 memory 的 Append + ReadState：
// 写 2 条 fact（evidence + boundary）、1 条 idea、1 条 hint；
// ReadState 返回的 JSON 必须包含 evidence/boundaries/hypotheses/hints 四个 key。
func TestStore_AppendAndReadState(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	e, err := s.LookupOrCreate(ctx, "default", "h", ModeProxy)
	if err != nil {
		t.Fatal(err)
	}

	if err := s.AppendFact(ctx, e.ID,
		[]byte(`{"category":"evidence","content":"endpoint X 401"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendFact(ctx, e.ID,
		[]byte(`{"category":"boundary","content":"all_similar at threshold 0.3"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendIdea(ctx, e.ID,
		[]byte(`{"direction":"GET /admin","status":"testing"}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendHint(ctx, e.ID,
		[]byte(`{"from_skill":"vuln-web-bac","content":"hint content","priority":7}`)); err != nil {
		t.Fatal(err)
	}

	state, err := s.ReadState(ctx, e.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := string(state)
	if !strings.Contains(got, `"evidence"`) ||
		!strings.Contains(got, `"boundaries"`) ||
		!strings.Contains(got, `"hypotheses"`) ||
		!strings.Contains(got, `"hints"`) {
		t.Fatalf("state missing keys: %s", got)
	}
}
