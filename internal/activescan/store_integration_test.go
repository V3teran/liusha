//go:build integration

package activescan

import (
	"context"
	"testing"
	"time"

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

// TestStore_Heartbeat 验证：Heartbeat 刷新 active 行的 heartbeat_at；终态行不受影响
// （WHERE status='active' 守卫，避免复活已结束的扫描）。
func TestStore_Heartbeat(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	sc, err := s.Create(ctx, "心跳测试 brief", "")
	if err != nil {
		t.Fatal(err)
	}
	// 人为回拨心跳到 1 小时前，再 Heartbeat 应刷新到接近 now。
	if _, err := s.pool.Exec(ctx,
		`UPDATE active_scan SET heartbeat_at = now() - interval '1 hour' WHERE id=$1`, sc.ID); err != nil {
		t.Fatalf("回拨心跳: %v", err)
	}
	if err := s.Heartbeat(ctx, sc.ID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if age := heartbeatAgeSeconds(ctx, t, s, sc.ID); age > 60 {
		t.Fatalf("Heartbeat 后心跳年龄=%.0fs，应刷新到 ~0", age)
	}

	// 终态行不该被 Heartbeat 复活：abort 后回拨心跳，Heartbeat 不应改 heartbeat_at。
	if err := s.Abort(ctx, sc.ID, "stop"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE active_scan SET heartbeat_at = now() - interval '1 hour' WHERE id=$1`, sc.ID); err != nil {
		t.Fatalf("回拨心跳2: %v", err)
	}
	if err := s.Heartbeat(ctx, sc.ID); err != nil {
		t.Fatalf("heartbeat on aborted: %v", err)
	}
	if age := heartbeatAgeSeconds(ctx, t, s, sc.ID); age < 600 {
		t.Fatalf("终态行不该被 Heartbeat 刷新，年龄=%.0fs 应仍 ~1h", age)
	}
}

// TestStore_ReapStale 验证：心跳超时的 active 扫描被判 aborted（写 ended_at + error_message），
// 心跳新鲜的扫描不动。
func TestStore_ReapStale(t *testing.T) {
	ctx := context.Background()
	s := NewStore(dbtest.NewPgPool(t))

	fresh, err := s.Create(ctx, "新鲜 brief", "")
	if err != nil {
		t.Fatal(err)
	}
	stale, err := s.Create(ctx, "超时 brief", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.pool.Exec(ctx,
		`UPDATE active_scan SET heartbeat_at = now() - interval '1 hour' WHERE id=$1`, stale.ID); err != nil {
		t.Fatalf("回拨 stale 心跳: %v", err)
	}

	n, err := s.ReapStale(ctx, 10*time.Minute)
	if err != nil {
		t.Fatalf("reap: %v", err)
	}
	if n < 1 {
		t.Fatalf("应至少回收 1 条超时扫描, got %d", n)
	}

	gotStale, err := s.GetByID(ctx, stale.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotStale.Status != StatusAborted {
		t.Fatalf("stale status=%s, want aborted", gotStale.Status)
	}
	if gotStale.EndedAt == nil {
		t.Fatalf("回收的扫描应写 ended_at")
	}
	if gotStale.ErrorMessage == "" {
		t.Fatalf("回收的扫描应写 error_message")
	}

	gotFresh, err := s.GetByID(ctx, fresh.ID)
	if err != nil {
		t.Fatal(err)
	}
	if gotFresh.Status != StatusActive {
		t.Fatalf("新鲜扫描 status=%s，不该被回收（want active）", gotFresh.Status)
	}
}

// heartbeatAgeSeconds 返回 active_scan 行心跳距 now 的秒数（测试辅助）。
func heartbeatAgeSeconds(ctx context.Context, t *testing.T, s *Store, id string) float64 {
	t.Helper()
	var age float64
	if err := s.pool.QueryRow(ctx,
		`SELECT extract(epoch from (now()-heartbeat_at)) FROM active_scan WHERE id=$1`, id).Scan(&age); err != nil {
		t.Fatalf("查询心跳年龄: %v", err)
	}
	return age
}
