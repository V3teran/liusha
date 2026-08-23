//go:build integration

package assignment

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
)

const testScenario = "web-pentest-killchain"

// TestStore_CreateThenGetByID 验证：建 assignment 后可按 ID 读回，字段（含 payload 序列化）一致。
func TestStore_CreateThenGetByID(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)

	asg, err := s.Create(context.Background(), NewParams{
		ScenarioID: testScenario,
		Source:     SourceManual,
		Items:      []Item{{Brief: "扫描 http://target.com"}},
		Title:      "手动下发",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if asg.ID == "" {
		t.Fatal("create 应返回非空 ID")
	}

	got, err := s.GetByID(context.Background(), asg.ID)
	if err != nil {
		t.Fatalf("get by id: %v", err)
	}
	if got.ScenarioID != testScenario || got.Source != SourceManual || got.Title != "手动下发" {
		t.Fatalf("字段不匹配: %+v", got)
	}
}

// TestStore_Create_RejectsMissingScenarioOrSource 验证：缺 scenario_id / 非法 source 建不出 assignment。
func TestStore_Create_RejectsMissingScenarioOrSource(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	if _, err := s.Create(ctx, NewParams{ScenarioID: "", Source: SourceManual}); err == nil {
		t.Fatal("缺 scenario_id 应报错")
	}
	if _, err := s.Create(ctx, NewParams{ScenarioID: testScenario, Source: "bogus"}); err == nil {
		t.Fatal("非法 source 应报错")
	}
}

// TestStore_List_FiltersByScenario 验证：List 按 scenario_id 过滤，且不传时不过滤。
func TestStore_List_FiltersByScenario(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ctx := context.Background()

	if _, err := s.Create(ctx, NewParams{ScenarioID: testScenario, Source: SourceManual, Title: "a1"}); err != nil {
		t.Fatalf("create scenario a: %v", err)
	}
	if _, err := s.Create(ctx, NewParams{ScenarioID: "traffic-analysis", Source: SourceAuto, Title: "p1"}); err != nil {
		t.Fatalf("create scenario b: %v", err)
	}

	filtered, err := s.List(ctx, testScenario, 0)
	if err != nil {
		t.Fatalf("list by scenario: %v", err)
	}
	for _, a := range filtered {
		if a.ScenarioID != testScenario {
			t.Fatalf("List(%q) 混入了 %s", testScenario, a.ScenarioID)
		}
	}

	all, err := s.List(ctx, "", 0)
	if err != nil {
		t.Fatalf("list all: %v", err)
	}
	if len(all) < 2 {
		t.Fatalf("List(\"\") 应至少含 2 条，got %d", len(all))
	}
}

// TestStore_DeriveStatus 验证 §3.2 派生规则：empty/running/done/aborted 四种子 task 组合。
func TestStore_DeriveStatus(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	s := NewStore(pool)
	ts := task.NewStore(pool)
	ctx := context.Background()

	// empty：无子 task
	empty, err := s.Create(ctx, NewParams{ScenarioID: testScenario, Source: SourceAuto})
	if err != nil {
		t.Fatalf("create empty assignment: %v", err)
	}
	st, err := s.DeriveStatus(ctx, empty.ID)
	if err != nil {
		t.Fatalf("derive empty: %v", err)
	}
	if st != StatusEmpty {
		t.Fatalf("无子 task 应为 empty，got %s", st)
	}

	// running：一个子 task 处于 active
	running, err := s.Create(ctx, NewParams{ScenarioID: testScenario, Source: SourceAuto})
	if err != nil {
		t.Fatalf("create running assignment: %v", err)
	}
	if _, err := ts.Create(ctx, task.NewParams{ScenarioID: testScenario, AssignmentID: running.ID, Brief: "分析 a.com"}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	st, err = s.DeriveStatus(ctx, running.ID)
	if err != nil {
		t.Fatalf("derive running: %v", err)
	}
	if st != StatusRunning {
		t.Fatalf("有 active 子 task 应为 running，got %s", st)
	}

	// done：子 task 全终态且含 completed
	done, err := s.Create(ctx, NewParams{ScenarioID: testScenario, Source: SourceAuto})
	if err != nil {
		t.Fatalf("create done assignment: %v", err)
	}
	doneTask, err := ts.Create(ctx, task.NewParams{ScenarioID: testScenario, AssignmentID: done.ID, Brief: "分析 b.com"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := ts.Complete(ctx, doneTask.ID); err != nil {
		t.Fatalf("complete task: %v", err)
	}
	st, err = s.DeriveStatus(ctx, done.ID)
	if err != nil {
		t.Fatalf("derive done: %v", err)
	}
	if st != StatusDone {
		t.Fatalf("全终态含 completed 应为 done，got %s", st)
	}

	// aborted：子 task 全终态且无 completed
	aborted, err := s.Create(ctx, NewParams{ScenarioID: testScenario, Source: SourceAuto})
	if err != nil {
		t.Fatalf("create aborted assignment: %v", err)
	}
	abortedTask, err := ts.Create(ctx, task.NewParams{ScenarioID: testScenario, AssignmentID: aborted.ID, Brief: "分析 c.com"})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := ts.Abort(ctx, abortedTask.ID, "test"); err != nil {
		t.Fatalf("abort task: %v", err)
	}
	st, err = s.DeriveStatus(ctx, aborted.ID)
	if err != nil {
		t.Fatalf("derive aborted: %v", err)
	}
	if st != StatusAborted {
		t.Fatalf("全终态无 completed 应为 aborted，got %s", st)
	}
}
