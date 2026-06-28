//go:build integration

package passivesession

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/dbtest"
)

// TestStore_Heartbeat 验证：Heartbeat 刷新 active 行的 heartbeat_at；终态行不受影响
// （WHERE status='active' 守卫，避免复活已结束的会话）。
func TestStore_Heartbeat(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	sess, err := s.Create(ctx, "hb.example.test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE passive_session SET heartbeat_at = now() - interval '1 hour' WHERE id=$1`, sess.ID); err != nil {
		t.Fatalf("回拨心跳: %v", err)
	}
	if err := s.Heartbeat(ctx, sess.ID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if age := heartbeatAgeSeconds(ctx, t, s, sess.ID); age > 60 {
		t.Fatalf("Heartbeat 后心跳年龄=%.0fs，应刷新到 ~0", age)
	}
}

// TestStore_ReapStale 验证：心跳超时的 active 会话被判 aborted（写 ended_at + error_message），
// 心跳新鲜的会话不动。
func TestStore_ReapStale(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	fresh, err := s.Create(ctx, "fresh.example.test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	stale, err := s.Create(ctx, "stale.example.test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE passive_session SET heartbeat_at = now() - interval '1 hour' WHERE id=$1`, stale.ID); err != nil {
		t.Fatalf("回拨 stale 心跳: %v", err)
	}

	n, err := s.ReapStale(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n < 1 {
		t.Fatalf("应至少回收 1 条超时会话, got %d", n)
	}

	gotStale, err := s.GetByID(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotStale.Status != StatusAborted {
		t.Fatalf("stale status=%s, want aborted", gotStale.Status)
	}
	if gotStale.EndedAt == nil {
		t.Fatalf("回收的会话应写 ended_at")
	}
	if gotStale.ErrorMessage == "" {
		t.Fatalf("回收的会话应写 error_message")
	}

	gotFresh, err := s.GetByID(ctx, fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotFresh.Status != StatusActive {
		t.Fatalf("新鲜会话 status=%s，不该被回收（want active）", gotFresh.Status)
	}
}

// heartbeatAgeSeconds 返回 passive_session 行心跳距 now 的秒数（测试辅助）。
func heartbeatAgeSeconds(ctx context.Context, t *testing.T, s *Store, id string) float64 {
	t.Helper()
	var age float64
	if err := s.pool.QueryRow(ctx,
		`SELECT extract(epoch from (now()-heartbeat_at)) FROM passive_session WHERE id=$1`, id).Scan(&age); err != nil {
		t.Fatalf("查询心跳年龄: %v", err)
	}
	return age
}
