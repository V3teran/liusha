//go:build integration

package task

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/dbtest"
)

// task/assignment 是任务模块核心域：此前 -short 0%（真库语义只有 scheduler
// 集成测试间接覆盖）。本文件直测 store 的状态机与筛选口径——真库（testcontainers
// + 全量迁移）保证 SQL 与生产一致。

func newStores(t *testing.T) (*Store, *assignment.Store) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	return NewStore(pool), assignment.NewStore(pool)
}

// mkAssignment 建 1 个含 N 个 item 的 assignment（batch 下发的固定前置）。
func mkAssignment(t *testing.T, s *assignment.Store, ctx context.Context, briefs ...string) string {
	t.Helper()
	items := make([]assignment.Item, 0, len(briefs))
	for _, b := range briefs {
		items = append(items, assignment.Item{Brief: b})
	}
	a, err := s.Create(ctx, assignment.NewParams{Source: assignment.SourceManual, Items: items, Title: "t"})
	if err != nil {
		t.Fatalf("create assignment: %v", err)
	}
	return a.ID
}

func TestCreateAndSummary(t *testing.T) {
	tasks, assignments := newStores(t)
	ctx := context.Background()

	asg := mkAssignment(t, assignments, ctx, "对 http://a.example.com 做存活探测", "对 http://b.example.com 做存活探测")
	for _, brief := range []string{"对 http://a.example.com 做存活探测", "对 http://b.example.com 做存活探测"} {
		if _, err := tasks.Create(ctx, NewParams{AssignmentID: asg, Brief: brief}); err != nil {
			t.Fatalf("create task: %v", err)
		}
	}

	// 批内序号 1..N 与 total：GetSummary 的派生列（窗口函数曾在此回归）
	rows, err := tasks.ListFiltered(ctx, ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("应有 2 个 task, got %d", len(rows))
	}
	for _, r := range rows {
		if r.AssignmentID != asg {
			t.Errorf("assignment_id 派生列不符: %+v", r)
		}
		if r.AssignmentTot != 2 {
			t.Errorf("assignment_total 应为 2: %+v", r)
		}
		if r.AssignmentNdx < 1 || r.AssignmentNdx > 2 {
			t.Errorf("assignment_ndx 应在 1..2: %+v", r)
		}
		if r.Source != "manual" {
			t.Errorf("source 派生应为 manual: %+v", r)
		}
	}
}

func TestStatusTransitions(t *testing.T) {
	tasks, assignments := newStores(t)
	ctx := context.Background()

	asg := mkAssignment(t, assignments, ctx, "对 http://c.example.com 做存活探测")
	tk, err := tasks.Create(ctx, NewParams{AssignmentID: asg, Brief: "对 http://c.example.com 做存活探测"})
	if err != nil {
		t.Fatal(err)
	}

	// active → completed
	if err := tasks.Complete(ctx, tk.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	got, _ := tasks.GetByID(ctx, tk.ID)
	if got.Status != StatusCompleted {
		t.Fatalf("应 completed, got %s", got.Status)
	}

	// 终态护栏：completed 后 abort 应被拒（保持既有终态）
	if err := tasks.Abort(ctx, tk.ID, "迟到的终止"); err == nil {
		t.Fatal("终态后 abort 应报错（CAS 守卫）")
	}
}

func TestListFilteredSearchAndSource(t *testing.T) {
	tasks, assignments := newStores(t)
	ctx := context.Background()

	asg := mkAssignment(t, assignments, ctx, "分析 10.0.0.1 的被动流量")
	if _, err := tasks.Create(ctx, NewParams{AssignmentID: asg, Brief: "分析 10.0.0.1 的被动流量"}); err != nil {
		t.Fatal(err)
	}

	// search 命中 brief
	rows, err := tasks.ListFiltered(ctx, ListFilter{Search: "10.0.0.1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("search 应命中 1 条, got %d", len(rows))
	}

	// source 筛选（manual 批次）
	rows, err = tasks.ListFiltered(ctx, ListFilter{Source: "manual", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("manual 筛选不应为空")
	}

	// CountFiltered 与列表同口径
	n, err := tasks.CountFiltered(ctx, ListFilter{Search: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("count 应为 1, got %d", n)
	}
}

func TestHeartbeatAndReaperView(t *testing.T) {
	tasks, assignments := newStores(t)
	ctx := context.Background()

	asg := mkAssignment(t, assignments, ctx, "对 http://d.example.com 做存活探测")
	tk, err := tasks.Create(ctx, NewParams{AssignmentID: asg, Brief: "对 http://d.example.com 做存活探测"})
	if err != nil {
		t.Fatal(err)
	}

	// 心跳推进 heartbeat_at（reaper 判活依据）
	if err := tasks.Heartbeat(ctx, tk.ID); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	got, _ := tasks.GetByID(ctx, tk.ID)
	if time.Since(got.HeartbeatAt) > time.Minute {
		t.Fatalf("heartbeat_at 应为刚写入的时刻: %+v", got.HeartbeatAt)
	}
}
