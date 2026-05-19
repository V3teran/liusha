//go:build integration

package agentrun

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/engagement"
)

// setup 启动一次性 Postgres，懒创建 engagement，返回 (Store, engagementID)。
func setup(t *testing.T) (*Store, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	es := engagement.NewStore(pool)
	e, err := es.LookupOrCreatePassiveSession(context.Background(), 24*time.Hour)
	if err != nil {
		t.Fatalf("lookup engagement: %v", err)
	}
	return NewStore(pool), e.ID
}

// TestStore_CreateThenComplete 验证：pending → running → done 完整生命周期。
func TestStore_CreateThenComplete(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	id, err := s.Create(ctx, NewParams{
		EngagementID: eid,
		Role:         "sniffer",
		Input:        json.RawMessage(`{"window_id":"w1"}`),
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get pending: %v", err)
	}
	if got.Status != StatusPending {
		t.Fatalf("初始 status 应为 pending, got %s", got.Status)
	}

	if err := s.SetRunning(ctx, id); err != nil {
		t.Fatalf("set running: %v", err)
	}
	res, _ := json.Marshal(map[string]any{"terminate_by": "done", "total_steps": 7})
	if err := s.SetDone(ctx, id, res); err != nil {
		t.Fatalf("set done: %v", err)
	}

	got, err = s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get done: %v", err)
	}
	if got.Status != StatusDone {
		t.Fatalf("最终 status 应为 done, got %s", got.Status)
	}
	var payload map[string]any
	if err := json.Unmarshal(got.Result, &payload); err != nil {
		t.Fatalf("result json invalid: %v", err)
	}
	if payload["terminate_by"] != "done" {
		t.Fatalf("result 内容不匹配: %+v", payload)
	}
}

// TestStore_SetError 验证：error 终态会把错误信息序列化进 result。
func TestStore_SetError(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	id, err := s.Create(ctx, NewParams{EngagementID: eid, Role: "sqli"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunning(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := s.SetError(ctx, id, "boom"); err != nil {
		t.Fatalf("set error: %v", err)
	}
	got, _ := s.GetByID(ctx, id)
	if got.Status != StatusError {
		t.Fatalf("status=%s, want error", got.Status)
	}
	var p map[string]string
	_ = json.Unmarshal(got.Result, &p)
	if p["error"] != "boom" {
		t.Fatalf("error 信息丢失: %+v", p)
	}
}

// TestStore_SetAborted 验证：aborted 终态可达。
func TestStore_SetAborted(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	id, err := s.Create(ctx, NewParams{EngagementID: eid, Role: "sniffer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetAborted(ctx, id); err != nil {
		t.Fatalf("set aborted: %v", err)
	}
	got, _ := s.GetByID(ctx, id)
	if got.Status != StatusAborted {
		t.Fatalf("status=%s, want aborted", got.Status)
	}
}

// TestStore_TerminalIsSticky 验证：done/error/aborted 终态后再 SetRunning 应该报错。
func TestStore_TerminalIsSticky(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	id, err := s.Create(ctx, NewParams{EngagementID: eid, Role: "sniffer"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetDone(ctx, id, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunning(ctx, id); err == nil {
		t.Fatalf("终态后 SetRunning 应失败")
	}
}

// TestStore_CreateWithParent 验证：NewParams.ParentID 写入 + GetByID 读出往返一致。
func TestStore_CreateWithParent(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	parentID, err := s.Create(ctx, NewParams{EngagementID: eid, Role: "hunter"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	childID, err := s.Create(ctx, NewParams{
		EngagementID: eid,
		Role:         "hunter",
		ParentID:     parentID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	got, err := s.GetByID(ctx, childID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if got.ParentID != parentID {
		t.Fatalf("child.ParentID=%q, want %q", got.ParentID, parentID)
	}

	// 父任务自己 ParentID 必须为空（独立/根任务）
	gotParent, err := s.GetByID(ctx, parentID)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if gotParent.ParentID != "" {
		t.Fatalf("parent.ParentID=%q, want empty", gotParent.ParentID)
	}
}
