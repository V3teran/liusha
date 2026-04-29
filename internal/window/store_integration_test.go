//go:build integration

package window

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

// setup 启动一次性 Postgres，懒创建 engagement，返回 (Store, engagementID)。
func setup(t *testing.T) (*Store, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	e, err := es.LookupOrCreate(context.Background(), "default", "h", engagement.ModeProxy)
	if err != nil {
		t.Fatalf("lookup engagement: %v", err)
	}
	return NewStore(pool), e.ID
}

// TestStore_OpenOrAppend_BatchTrigger 验证：连续追加到达 batchLimit 时
// 当前 open window 自动 close，新窗口在下一次 append 时打开。
func TestStore_OpenOrAppend_BatchTrigger(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	for i := 0; i < 4; i++ {
		if _, _, err := s.OpenOrAppend(ctx, eid, FlowRef{ID: int64(i + 1), URL: "/x"}, 3); err != nil {
			t.Fatalf("OpenOrAppend #%d: %v", i, err)
		}
	}
	closed, err := s.ListClosed(ctx, eid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 || len(closed[0].Flows) != 3 {
		t.Fatalf("expected 1 closed window with 3 flows, got %+v", closed)
	}
}

// TestStore_OpenOrAppend_ReturnsClosedID 验证：达到 batchLimit 那一次调用
// 返回 closed=true，且返回的 windowID 与 ListClosed 看到的一致。
func TestStore_OpenOrAppend_ReturnsClosedID(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	var lastID string
	var closedFlag bool
	for i := 0; i < 3; i++ {
		id, c, err := s.OpenOrAppend(ctx, eid, FlowRef{ID: int64(i + 1)}, 3)
		if err != nil {
			t.Fatal(err)
		}
		lastID, closedFlag = id, c
	}
	if !closedFlag {
		t.Fatalf("3rd append should trigger close, got closed=false")
	}
	closed, _ := s.ListClosed(ctx, eid, 10)
	if len(closed) != 1 || closed[0].ID != lastID {
		t.Fatalf("returned id %s 与 ListClosed 不一致: %+v", lastID, closed)
	}
}

// TestStore_MarkConsumed 验证：consumed 状态后不再出现在 ListClosed 中。
func TestStore_MarkConsumed(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	for i := 0; i < 3; i++ {
		if _, _, err := s.OpenOrAppend(ctx, eid, FlowRef{ID: int64(i + 1)}, 3); err != nil {
			t.Fatal(err)
		}
	}
	closed, _ := s.ListClosed(ctx, eid, 10)
	if len(closed) != 1 {
		t.Fatalf("expected 1 closed window, got %d", len(closed))
	}
	if err := s.MarkConsumed(ctx, closed[0].ID); err != nil {
		t.Fatal(err)
	}
	again, _ := s.ListClosed(ctx, eid, 10)
	if len(again) != 0 {
		t.Fatalf("consumed window should not appear in ListClosed: %+v", again)
	}
}

// TestStore_OpenOrAppend_SingleOpenWindow 验证：同一 engagement 同时只有 1 个 open window。
// 反复 append 但不达到 batchLimit，open 行始终只有 1 行。
func TestStore_OpenOrAppend_SingleOpenWindow(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	for i := 0; i < 5; i++ {
		if _, _, err := s.OpenOrAppend(ctx, eid, FlowRef{ID: int64(i + 1)}, 100); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM traffic_window WHERE engagement_id=$1 AND status='open'`, eid).
		Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("expected exactly 1 open window, got %d", n)
	}
}

// TestStore_CloseExpired 验证：过期的 open window 被批量置为 closed。
func TestStore_CloseExpired(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	if _, _, err := s.OpenOrAppend(ctx, eid, FlowRef{ID: 1}, 100); err != nil {
		t.Fatal(err)
	}
	// 直接把 started_at 拨回 10 分钟前，触发过期。
	if _, err := s.pool.Exec(ctx,
		`UPDATE traffic_window SET started_at = now() - interval '10 minutes' WHERE engagement_id=$1`, eid); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseExpired(ctx, eid, 60); err != nil {
		t.Fatal(err)
	}
	closed, _ := s.ListClosed(ctx, eid, 10)
	if len(closed) != 1 {
		t.Fatalf("expected 1 expired→closed window, got %d", len(closed))
	}
}
