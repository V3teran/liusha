//go:build integration

package dispatch

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// newTestScheduler 起一次性 Postgres（dbtest）+ miniredis（asynq enqueue），
// 组一份贴近生产装配的 Scheduler；返回 pool 供测试直接改 next_run_at 模拟"已到点"。
func newTestScheduler(t *testing.T) (*Scheduler, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	t.Cleanup(func() { _ = enq.Close() })

	assignments := assignment.NewStore(pool)
	tasks := task.NewStore(pool)
	agentRuns := agentrun.NewStore(pool)
	conversations := conversation.NewStore(pool)
	r := &Scheduler{
		Schedules:     cronschedule.NewStore(pool),
		Assignments:   assignments,
		Tasks:         tasks,
		AgentRuns:     agentRuns,
		Enq:           enq,
		Conversations: conversations,
		// Dispatcher 与 Scheduler 共用同一 conversation store：brief 条目展开时
		// expandItem 要把任务链到会话（生产装配同源，见 cmd/api）。
		Dispatcher: &Service{
			Assignments:   assignments,
			Tasks:         tasks,
			AgentRuns:     agentRuns,
			Conversations: conversations,
			Enq:           enq,
			MaxRunTimeout: time.Minute,
		},
		Logger: zerolog.Nop(),
	}
	return r, pool
}

// forceDue 把模板的 next_run_at 回拨到过去，模拟"已到点"（不等真实 cron 周期）。
func forceDue(t *testing.T, pool *pgxpool.Pool, scheduleID string) {
	t.Helper()
	if _, err := pool.Exec(context.Background(),
		"UPDATE cron_schedule SET next_run_at=$1 WHERE id=$2", time.Now().Add(-time.Minute), scheduleID); err != nil {
		t.Fatalf("回拨 next_run_at: %v", err)
	}
}

// TestFireDue_BriefItem_ExpandsTaskAndAgent 验证定时下发核心场景：brief 条目的定时模板到点 →
// 克隆 assignment → 展开 task + agent run + enqueue，且 next_run_at 推进到未来。
func TestFireDue_BriefItem_ExpandsTaskAndAgent(t *testing.T) {
	r, pool := newTestScheduler(t)
	ctx := context.Background()

	items := []assignment.Item{{Brief: "夜间复扫 http://target.com"}}
	sched, err := r.Schedules.Create(ctx, cronschedule.NewParams{
		CronExpr: "* * * * *", Items: items, Title: "nightly",
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	forceDue(t, pool, sched.ID)

	r.fireDue(ctx)

	got, err := r.Schedules.GetByID(ctx, sched.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if got.LastRunAt == nil {
		t.Fatal("触发后 last_run_at 应已回写")
	}
	if got.NextRunAt == nil || !got.NextRunAt.After(time.Now()) {
		t.Fatalf("触发后 next_run_at 应推进到未来，got %v", got.NextRunAt)
	}

	tasks, err := r.Tasks.ListFiltered(ctx, task.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	var found *task.Summary
	for i := range tasks {
		if tasks[i].Brief == items[0].Brief {
			found = &tasks[i]
		}
	}
	if found == nil {
		t.Fatalf("应展开出 brief=%q 的 task，got %+v", items[0].Brief, tasks)
	}
	if found.AssignmentID == "" {
		t.Fatal("展开的 task 应挂到克隆出的 assignment 上")
	}
	asg, err := r.Assignments.GetByID(ctx, found.AssignmentID)
	if err != nil {
		t.Fatalf("get cloned assignment: %v", err)
	}
	if asg.ScheduleID == nil || *asg.ScheduleID != sched.ID {
		t.Fatalf("克隆的 assignment 应回指模板 id，got %+v", asg.ScheduleID)
	}
	// 定时模板的条目是用户编写的目标描述：克隆批次的来源是人工下发（direct），
	// 与任务表、对话列表、漏洞筛选读到的是同一个真实值。
	if asg.Source != assignment.SourceDirect {
		t.Fatalf("定时克隆的 assignment 来源应为 direct，got %q", asg.Source)
	}
	if found.Source != string(assignment.SourceDirect) {
		t.Fatalf("任务列表应直接读出 direct 来源，got %q", found.Source)
	}

	runs, err := r.AgentRuns.ListByTask(ctx, found.ID, 10)
	if err != nil {
		t.Fatalf("list executor runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Role != "planner" {
		t.Fatalf("应建出 1 条 planner executor run，got %+v", runs)
	}

}

// TestFireDue_DisabledSchedule_NotFired 验证：停用的模板即使到点也不触发。
func TestFireDue_DisabledSchedule_NotFired(t *testing.T) {
	r, pool := newTestScheduler(t)
	ctx := context.Background()

	items := []assignment.Item{{Brief: "should not fire"}}
	sched, err := r.Schedules.Create(ctx, cronschedule.NewParams{
		CronExpr: "* * * * *", Items: items,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if _, err := r.Schedules.SetEnabled(ctx, sched.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	forceDue(t, pool, sched.ID)

	r.fireDue(ctx)

	tasks, err := r.Tasks.ListFiltered(ctx, task.ListFilter{Limit: 10})
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.Brief == items[0].Brief {
			t.Fatalf("停用的模板不应展开出 task: %+v", tk)
		}
	}
}
