//go:build integration

package agentrun

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
)

// setup 启动一次性 Postgres，建一个代理捕获 task 作为外键归属，返回 (Store, taskID)。
func setup(t *testing.T) (*Store, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	ts := task.NewStore(pool)
	tk, err := ts.Create(context.Background(), task.NewParams{

		AssignmentID: dbtest.SeedAssignment(t, pool),
		Brief:        "test agent run",
		TargetHost:   "test.example.com",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return NewStore(pool), tk.ID
}

// TestStore_CreateThenComplete 验证：pending → running → completed 完整生命周期。
func TestStore_CreateThenComplete(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	id, err := s.Create(ctx, NewParams{
		TaskID: taskID,
		Role:   "planner",
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
	if err := s.SetCompleted(ctx, id, res); err != nil {
		t.Fatalf("set done: %v", err)
	}

	got, err = s.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("get done: %v", err)
	}
	if got.Status != StatusCompleted {
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
	id, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "planner"})
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
	id, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "planner"})
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
	id, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "planner"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetCompleted(ctx, id, json.RawMessage(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRunning(ctx, id); err == nil {
		t.Fatalf("终态后 SetRunning 应失败")
	}
}

// TestStore_ListByTask 验证：ListByTask 取回 task 下所有 agent run。
// 同任务可有任意多个 run，但同时至多一个存活（部分唯一索引）——顺序创建、
// 每个先落终态再建下一个。
func TestStore_ListByTask(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	for i := 0; i < 3; i++ {
		id, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "planner"})
		if err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
		if err := s.SetAborted(ctx, id); err != nil {
			t.Fatalf("seed %d abort: %v", i, err)
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

// TestCreate_OneLiveRunPerTask 验证不变式（部分唯一索引 agent_run_one_live_
// per_task）：同任务同时至多一个 pending/running run。agent run 有四个并发
// 生产者（下发展开/会话续接/暂停后继续/恢复监控重投），「先查后建」的纪律
// 挡不住跨进程竞态——双存活 run 即双认知循环并发（双倍 LLM 账单 + 探索图
// 双写），违例写入由数据库直接拒绝、以哨兵 ErrLiveRunExists 上抛。
func TestCreate_OneLiveRunPerTask(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	runID, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "planner"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	// pending 存活期间：第二个存活 run 被拒。
	if _, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "planner"}); !errors.Is(err, ErrLiveRunExists) {
		t.Fatalf("pending 期间再建应返回 ErrLiveRunExists: %v", err)
	}

	// 推进到 running：仍是存活态，同样被拒。
	if err := s.SetRunning(ctx, runID); err != nil {
		t.Fatalf("set running: %v", err)
	}
	if _, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "planner"}); !errors.Is(err, ErrLiveRunExists) {
		t.Fatalf("running 期间再建应返回 ErrLiveRunExists: %v", err)
	}

	// 终止后槽位释放：可再建（暂停继续/恢复重投依赖此语义）。
	if err := s.SetAborted(ctx, runID); err != nil {
		t.Fatalf("set aborted: %v", err)
	}
	if _, err := s.Create(ctx, NewParams{TaskID: taskID, Role: "planner"}); err != nil {
		t.Fatalf("终态后应可再建: %v", err)
	}
}
