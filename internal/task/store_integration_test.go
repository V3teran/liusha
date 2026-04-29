//go:build integration

package task

import (
	"context"
	"encoding/json"
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

// TestStore_ParentChild 验证：spawn_subtask 时 parent_task_id 正确写入，
// 父任务可通过 ListByEngagement 一并看到。
func TestStore_ParentChild(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)

	parentID, err := s.Create(ctx, NewParams{EngagementID: eid, Role: "bac"})
	if err != nil {
		t.Fatal(err)
	}
	childID, err := s.Create(ctx, NewParams{
		EngagementID: eid,
		ParentTaskID: &parentID,
		Role:         "sqli",
	})
	if err != nil {
		t.Fatal(err)
	}

	child, _ := s.GetByID(ctx, childID)
	if child.ParentTaskID == nil || *child.ParentTaskID != parentID {
		t.Fatalf("child.ParentTaskID 不匹配: %+v want %s", child.ParentTaskID, parentID)
	}

	list, err := s.ListByEngagement(ctx, eid, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 tasks, got %d", len(list))
	}
}

// TestStore_InflightCounters 验证：父子并发计数器在状态推进时正确收敛。
func TestStore_InflightCounters(t *testing.T) {
	ctx := context.Background()
	s, eid := setup(t)
	parentID, _ := s.Create(ctx, NewParams{EngagementID: eid, Role: "bac"})
	c1, _ := s.Create(ctx, NewParams{EngagementID: eid, ParentTaskID: &parentID, Role: "sqli"})
	_, _ = s.Create(ctx, NewParams{EngagementID: eid, ParentTaskID: &parentID, Role: "sqli"})

	n, err := s.CountInflightChildren(ctx, parentID)
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("inflight children=%d, want 2", n)
	}

	if err := s.SetRunning(ctx, c1); err != nil {
		t.Fatal(err)
	}
	if err := s.SetDone(ctx, c1, json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}

	n, _ = s.CountInflightChildren(ctx, parentID)
	if n != 1 {
		t.Fatalf("after one done, inflight=%d, want 1", n)
	}

	total, _ := s.CountInflightInEngagement(ctx, eid)
	// parent (pending) + 1 remaining child (pending) = 2
	if total != 2 {
		t.Fatalf("engagement inflight=%d, want 2", total)
	}
}
