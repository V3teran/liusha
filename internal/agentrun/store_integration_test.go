//go:build integration

package agentrun

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
)

// setup 启动一次性 Postgres，建一个 passive task 作为外键归属，返回 (Store, taskID)。
func setup(t *testing.T) (*Store, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	ts := task.NewStore(pool)
	tk, err := ts.Create(context.Background(), task.NewParams{
		
		AssignmentID: dbtest.SeedAssignment(t, pool, "api-pentest"),
		TargetHost:   "test.example.com",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return NewStore(pool), tk.ID
}

// TestStore_CreateThenComplete 验证：pending → running → done 完整生命周期。
func TestStore_CreateThenComplete(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	id, err := s.Create(ctx, NewParams{
		TaskID: taskID,
		Role:   "traffic-analysis",
		Input:  json.RawMessage(`{"window_id":"w1"}`),
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
	if got.TaskID != taskID {
		t.Fatalf("TaskID 应回填, got %q want %q", got.TaskID, taskID)
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
	s, taskID := setup(t)
	id, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "traffic-analysis"})
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
	s, taskID := setup(t)
	id, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "traffic-analysis"})
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
	s, taskID := setup(t)
	id, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "traffic-analysis"})
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

// TestStore_CreateWithParent 验证：NewParams.plannerID 写入 + GetByID 读出往返一致。
func TestStore_CreateWithParent(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	parentID, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "traffic-analysis"})
	if err != nil {
		t.Fatalf("create parent: %v", err)
	}

	childID, err := s.Create(ctx, NewParams{
		TaskID:         taskID,
		Role:           "traffic-analysis",
		plannerID: parentID,
	})
	if err != nil {
		t.Fatalf("create child: %v", err)
	}

	got, err := s.GetByID(ctx, childID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if got.plannerID != parentID {
		t.Fatalf("child.plannerID=%q, want %q", got.plannerID, parentID)
	}

	// planner自己 plannerID 必须为空（独立/根任务）
	gotParent, err := s.GetByID(ctx, parentID)
	if err != nil {
		t.Fatalf("get parent: %v", err)
	}
	if gotParent.plannerID != "" {
		t.Fatalf("parent.plannerID=%q, want empty", gotParent.plannerID)
	}
}

// TestStore_ListByTask 验证：ListByTask 取回 task 下所有 agent run（取代旧 ListByOwner）。
func TestStore_ListByTask(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	for i := 0; i < 3; i++ {
		if _, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "traffic-analysis"}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}

	runs, err := s.ListByTask(ctx, taskID, 100)
	if err != nil {
		t.Fatalf("list by task: %v", err)
	}
	if len(runs) != 3 {
		t.Fatalf("应读回 3 行，得到 %d", len(runs))
	}
	for _, r := range runs {
		if r.TaskID != taskID {
			t.Fatalf("run.TaskID=%q, want %q", r.TaskID, taskID)
		}
	}
}
