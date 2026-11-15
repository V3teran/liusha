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
	a, err := s.Create(ctx, assignment.NewParams{Source: assignment.SourceDirect, Items: items, Title: "t"})
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
		if _, err := tasks.Create(ctx, NewParams{AssignmentID: asg, Goal: brief}); err != nil {
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
		if r.AssignmentTotal != 2 {
			t.Errorf("assignment_total 应为 2: %+v", r)
		}
		if r.AssignmentIndex < 1 || r.AssignmentIndex > 2 {
			t.Errorf("assignment_index 应在 1..2: %+v", r)
		}
		if r.Source != "direct" {
			t.Errorf("source 派生应为 direct: %+v", r)
		}
	}

	// GetSummary 是详情板的数据源，列序与 scanSummary 17 目标一致——内联 SELECT
	// 曾漂移缺列（15 vs 17）打挂整个详情板，此断言钉住两查询同形状。
	first := rows[0]
	if _, err := tasks.MarkPaused(ctx, first.ID); err != nil {
		t.Fatalf("mark paused: %v", err)
	}
	got, err := tasks.GetSummary(ctx, first.ID)
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	if !got.Paused {
		t.Errorf("paused 派生列应为 true: %+v", got)
	}
	if got.AssignmentTitle != "t" {
		t.Errorf("assignment_title 应取 assignment.title: %+v", got)
	}
	if got.AssignmentTotal != 2 || got.AssignmentIndex < 1 || got.AssignmentIndex > 2 {
		t.Errorf("批内序号/总数派生不符: %+v", got)
	}
	if got.ID != first.ID || got.Goal != first.Goal || got.Status != first.Status {
		t.Errorf("GetSummary 与 ListFiltered 本体字段不一致: %+v vs %+v", got, first)
	}
}

func TestStatusTransitions(t *testing.T) {
	tasks, assignments := newStores(t)
	ctx := context.Background()

	asg := mkAssignment(t, assignments, ctx, "对 http://c.example.com 做存活探测")
	tk, err := tasks.Create(ctx, NewParams{AssignmentID: asg, Goal: "对 http://c.example.com 做存活探测"})
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
	if _, err := tasks.Abort(ctx, tk.ID, "迟到的终止"); err == nil {
		t.Fatal("终态后 abort 应报错（CAS 守卫）")
	}
}

func TestListFilteredSearchAndSource(t *testing.T) {
	tasks, assignments := newStores(t)
	ctx := context.Background()

	asg := mkAssignment(t, assignments, ctx, "分析 10.0.0.1 的代理流量")
	if _, err := tasks.Create(ctx, NewParams{AssignmentID: asg, Goal: "分析 10.0.0.1 的代理流量"}); err != nil {
		t.Fatal(err)
	}

	// search 命中目标
	rows, err := tasks.ListFiltered(ctx, ListFilter{Search: "10.0.0.1", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("search 应命中 1 条, got %d", len(rows))
	}

	// source 筛选（direct 批次）
	rows, err = tasks.ListFiltered(ctx, ListFilter{Source: "direct", Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) == 0 {
		t.Fatal("direct 筛选不应为空")
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
	tk, err := tasks.Create(ctx, NewParams{AssignmentID: asg, Goal: "对 http://d.example.com 做存活探测"})
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

// TestSummaryScheduleLink 验证：定时模板克隆的批次，任务摘要带回来源模板 id 与 cron
// （任务详情据此跳到模板管理）；列表与详情两条查询同口径。普通批次两列为空。
func TestSummaryScheduleLink(t *testing.T) {
	pool := dbtest.NewPgPool(t)
	tasks, assignments := NewStore(pool), assignment.NewStore(pool)
	ctx := context.Background()

	var scheduleID string
	if err := pool.QueryRow(ctx,
		`INSERT INTO cron_schedule (cron_expr, payload, title) VALUES ('0 4 * * *', '[]', 's') RETURNING id::text`,
	).Scan(&scheduleID); err != nil {
		t.Fatalf("建模板: %v", err)
	}
	asg, err := assignments.Create(ctx, assignment.NewParams{
		Source: assignment.SourceDirect, Items: []assignment.Item{{Brief: "b"}}, ScheduleID: &scheduleID,
	})
	if err != nil {
		t.Fatalf("建定时批次: %v", err)
	}
	scheduled, err := tasks.Create(ctx, NewParams{AssignmentID: asg.ID, Goal: "b"})
	if err != nil {
		t.Fatalf("建任务: %v", err)
	}
	plain, err := tasks.Create(ctx, NewParams{AssignmentID: mkAssignment(t, assignments, ctx, "p"), Goal: "p"})
	if err != nil {
		t.Fatalf("建普通任务: %v", err)
	}

	got, err := tasks.GetSummary(ctx, scheduled.ID)
	if err != nil {
		t.Fatalf("get summary: %v", err)
	}
	if got.ScheduleID != scheduleID || got.CronExpr != "0 4 * * *" {
		t.Fatalf("详情应带回模板 id 与 cron: %+v", got)
	}
	rows, err := tasks.ListFiltered(ctx, ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, r := range rows {
		switch r.ID {
		case scheduled.ID:
			if r.ScheduleID != scheduleID {
				t.Errorf("列表应带回模板 id: %+v", r)
			}
		case plain.ID:
			if r.ScheduleID != "" || r.CronExpr != "" {
				t.Errorf("普通任务不应带模板信息: %+v", r)
			}
		}
	}
}

// 搜索词里的 % _ 按字面匹配：搜「user_id」不应命中「userXid」。
func TestListFilteredSearchIsLiteral(t *testing.T) {
	tasks, assignments := newStores(t)
	ctx := context.Background()
	asg := mkAssignment(t, assignments, ctx, "x")
	for _, goal := range []string{"测试 user_id 参数", "测试 userXid 参数", "折扣 100% 的接口"} {
		if _, err := tasks.Create(ctx, NewParams{AssignmentID: asg, Goal: goal}); err != nil {
			t.Fatal(err)
		}
	}
	for q, want := range map[string]int{"user_id": 1, "100%": 1, "测试": 2} {
		n, err := tasks.CountFiltered(ctx, ListFilter{Search: q})
		if err != nil {
			t.Fatal(err)
		}
		if n != want {
			t.Errorf("搜索 %q 命中 %d 条，期望 %d", q, n, want)
		}
	}
}

// 工作空间目录的显示名：批次取标题（没写标题时取首个任务的目标），任务取目标；查不到的 ID 不出现。
func TestDirectoryLabels(t *testing.T) {
	tasks, assignments := newStores(t)
	ctx := context.Background()
	untitled, err := assignments.Create(ctx, assignment.NewParams{Source: assignment.SourceDirect, Items: []assignment.Item{{Brief: "x"}}})
	if err != nil {
		t.Fatal(err)
	}
	titled, err := assignments.Create(ctx, assignment.NewParams{Source: assignment.SourceDirect, Items: []assignment.Item{{Brief: "y"}}, Title: "夜间复扫"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := tasks.Create(ctx, NewParams{AssignmentID: untitled.ID, Goal: "首个任务的目标"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tasks.Create(ctx, NewParams{AssignmentID: untitled.ID, Goal: "第二个任务"}); err != nil {
		t.Fatal(err)
	}
	const missing = "33333333-3333-4333-8333-333333333333"
	asgLabels, taskLabels, err := tasks.DirectoryLabels(ctx, []string{untitled.ID, titled.ID, missing}, []string{first.ID, missing})
	if err != nil {
		t.Fatal(err)
	}
	if asgLabels[untitled.ID] != "首个任务的目标" || asgLabels[titled.ID] != "夜间复扫" {
		t.Errorf("批次显示名不符: %v", asgLabels)
	}
	if taskLabels[first.ID] != "首个任务的目标" {
		t.Errorf("任务显示名不符: %v", taskLabels)
	}
	if _, ok := asgLabels[missing]; ok {
		t.Error("查不到的 ID 不应出现")
	}
}
