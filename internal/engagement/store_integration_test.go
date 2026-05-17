//go:build integration

package engagement

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestStore_LookupOrCreatePassiveSession_LazyAndIdempotent 验证：
// 首次调用插入新 passive session；二次调用幂等返回同一行（active 唯一索引保证）。
func TestStore_LookupOrCreatePassiveSession_LazyAndIdempotent(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	got, err := s.LookupOrCreatePassiveSession(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" || got.Status != StatusActive || got.Mode != ModePassive {
		t.Fatalf("unexpected: %+v", got)
	}
	if got.ExpiresAt == nil {
		t.Fatal("passive session 必须有 expires_at")
	}
	got2, err := s.LookupOrCreatePassiveSession(ctx, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != got2.ID {
		t.Fatalf("expected idempotent, got %s vs %s", got.ID, got2.ID)
	}
}

// TestStore_Abort 验证：Abort 后 status=aborted；新 LookupOrCreatePassiveSession 会插入新 active 行。
func TestStore_Abort(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	e, err := s.LookupOrCreatePassiveSession(ctx, 24*time.Hour)
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
	e2, err := s.LookupOrCreatePassiveSession(ctx, 24*time.Hour)
	if err != nil || e2.ID == e.ID {
		t.Fatalf("expected new active, got %+v err=%v", e2, err)
	}
}

// TestStore_CreateActiveSession_Basic 验证 active engagement 基本写入：
// mode=active / status=active / expires_at=nil / scope 透传。
func TestStore_CreateActiveSession_Basic(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	scope := json.RawMessage(`{"brief":"测 /admin BAC，账号 admin/x"}`)
	got, err := s.CreateActiveSession(ctx, scope)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == "" {
		t.Fatal("active engagement id 空")
	}
	if got.Mode != ModeActive {
		t.Fatalf("mode=%s, want active", got.Mode)
	}
	if got.Status != StatusActive {
		t.Fatalf("status=%s, want active", got.Status)
	}
	if got.ExpiresAt != nil {
		t.Fatalf("active engagement 不应设 expires_at, got %v", got.ExpiresAt)
	}
	if string(got.Scope) != string(scope) {
		t.Fatalf("scope mismatch: got %s, want %s", got.Scope, scope)
	}
}

// TestStore_CreateActiveSession_ConcurrentMultiple 验证：active engagement 不受
// engagement_active_passive_uniq 唯一索引限制，可同时存在多个 active 行。
func TestStore_CreateActiveSession_ConcurrentMultiple(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	e1, err := s.CreateActiveSession(ctx, json.RawMessage(`{"brief":"扫 a.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	e2, err := s.CreateActiveSession(ctx, json.RawMessage(`{"brief":"扫 b.com"}`))
	if err != nil {
		t.Fatal(err)
	}
	if e1.ID == e2.ID {
		t.Fatalf("expected distinct active engagements, got same id %s", e1.ID)
	}
}

// TestStore_CreateActiveSession_EmptyScope 验证：scope 必填。
func TestStore_CreateActiveSession_EmptyScope(t *testing.T) {
	ctx := context.Background()
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	if _, err := s.CreateActiveSession(ctx, nil); err == nil {
		t.Fatal("scope=nil 应报错")
	}
}
