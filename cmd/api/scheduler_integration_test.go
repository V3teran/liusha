//go:build integration

package main

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/hunterrun"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
)

// newTestCronRunner 起一次性 Postgres（dbtest）+ miniredis（asynq enqueue），
// 组一份贴近生产装配的 cronRunner；返回 pool 供测试直接改 next_run_at 模拟"已到点"。
func newTestCronRunner(t *testing.T) (*cronRunner, *pgxpool.Pool) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	mr := miniredis.RunT(t)
	enq := worker.NewClient(asynq.RedisClientOpt{Addr: mr.Addr()})
	t.Cleanup(func() { _ = enq.Close() })

	assignments := assignment.NewStore(pool)
	tasks := task.NewStore(pool)
	hunters := hunterrun.NewStore(pool)
	r := &cronRunner{
		schedules:   cronschedule.NewStore(pool),
		assignments: assignments,
		tasks:       tasks,
		proxyFlows:  traffic.NewProxyStore(pool),
		hunters:     hunters,
		enq:         enq,
		scan: &scanAdapter{
			assignments:   assignments,
			tasks:         tasks,
			hunters:       hunters,
			enq:           enq,
			maxRunTimeout: time.Minute,
		},
		logger: zerolog.Nop(),
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

// TestFireDue_ActiveSchedule_ExpandsTaskAndHunter 验证 P4 核心场景：active 定时模板到点 →
// 克隆 assignment → 展开 task + hunter run + enqueue，且 next_run_at 推进到未来。
func TestFireDue_ActiveSchedule_ExpandsTaskAndHunter(t *testing.T) {
	r, pool := newTestCronRunner(t)
	ctx := context.Background()

	items := []assignment.Item{{Brief: "夜间复扫 http://target.com"}}
	sched, err := r.schedules.Create(ctx, cronschedule.NewParams{
		ScenarioID: "web-pentest", CronExpr: "* * * * *", Items: items, Title: "nightly",
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	forceDue(t, pool, sched.ID)

	r.fireDue(ctx)

	got, err := r.schedules.GetByID(ctx, sched.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if got.LastRunAt == nil {
		t.Fatal("触发后 last_run_at 应已回写")
	}
	if got.NextRunAt == nil || !got.NextRunAt.After(time.Now()) {
		t.Fatalf("触发后 next_run_at 应推进到未来，got %v", got.NextRunAt)
	}

	tasks, err := r.tasks.List(ctx, "web-pentest", 10)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	var found *task.Task
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
	asg, err := r.assignments.GetByID(ctx, found.AssignmentID)
	if err != nil {
		t.Fatalf("get cloned assignment: %v", err)
	}
	if asg.ScheduleID == nil || *asg.ScheduleID != sched.ID {
		t.Fatalf("克隆的 assignment 应回指模板 id，got %+v", asg.ScheduleID)
	}

	runs, err := r.hunters.ListByTask(ctx, found.ID, 10)
	if err != nil {
		t.Fatalf("list hunter runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Role != "orchestrator" {
		t.Fatalf("应建出 1 条 orchestrator hunter run，got %+v", runs)
	}
}

// TestFireDue_PassiveSchedule_ClaimsUnconsumedTraffic 验证 api-pentest 定时模板：领取该 host
// 未消费的 proxy_traffic 后展开 task + orchestrator hunter run。
func TestFireDue_PassiveSchedule_ClaimsUnconsumedTraffic(t *testing.T) {
	r, pool := newTestCronRunner(t)
	ctx := context.Background()

	host := "target.com"
	if _, err := r.proxyFlows.Append(ctx, traffic.ProxyTraffic{Host: host, Method: "GET", URL: "http://" + host + "/"}); err != nil {
		t.Fatalf("append proxy_traffic: %v", err)
	}

	items := []assignment.Item{{Host: host}}
	sched, err := r.schedules.Create(ctx, cronschedule.NewParams{
		ScenarioID: "api-pentest", CronExpr: "* * * * *", Items: items, Title: "recheck-" + host,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	forceDue(t, pool, sched.ID)

	r.fireDue(ctx)

	tasks, err := r.tasks.List(ctx, "api-pentest", 10)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	var found *task.Task
	for i := range tasks {
		if tasks[i].TargetHost == host {
			found = &tasks[i]
		}
	}
	if found == nil {
		t.Fatalf("应展开出 host=%q 的 passive task，got %+v", host, tasks)
	}

	runs, err := r.hunters.ListByTask(ctx, found.ID, 10)
	if err != nil {
		t.Fatalf("list hunter runs: %v", err)
	}
	if len(runs) != 1 || runs[0].Role != "orchestrator" {
		t.Fatalf("应建出 1 条 orchestrator hunter run，got %+v", runs)
	}
}

// TestFireDue_PassiveSchedule_NoTraffic_AbortsWithoutError 验证：host 当前无未消费流量时，
// 展开产生的空转 task 被 abort，且不阻塞其余到点模板（fireOne 不报错）。
func TestFireDue_PassiveSchedule_NoTraffic_AbortsWithoutError(t *testing.T) {
	r, pool := newTestCronRunner(t)
	ctx := context.Background()

	items := []assignment.Item{{Host: "never-captured.com"}}
	sched, err := r.schedules.Create(ctx, cronschedule.NewParams{
		ScenarioID: "api-pentest", CronExpr: "* * * * *", Items: items,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	forceDue(t, pool, sched.ID)

	if err := r.fireOne(ctx, sched); err != nil {
		t.Fatalf("无流量不应算 fireOne 失败: %v", err)
	}

	tasks, err := r.tasks.List(ctx, "api-pentest", 10)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	var found *task.Task
	for i := range tasks {
		if tasks[i].TargetHost == items[0].Host {
			found = &tasks[i]
		}
	}
	if found == nil {
		t.Fatal("应仍建出 task（随后 abort）")
	}
	if found.Status != task.StatusAborted {
		t.Fatalf("无未消费流量的 task 应 aborted，got %s", found.Status)
	}
}

// TestFireDue_DisabledSchedule_NotFired 验证：停用的模板即使到点也不触发。
func TestFireDue_DisabledSchedule_NotFired(t *testing.T) {
	r, pool := newTestCronRunner(t)
	ctx := context.Background()

	items := []assignment.Item{{Brief: "should not fire"}}
	sched, err := r.schedules.Create(ctx, cronschedule.NewParams{
		ScenarioID: "web-pentest", CronExpr: "* * * * *", Items: items,
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if err := r.schedules.SetEnabled(ctx, sched.ID, false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	forceDue(t, pool, sched.ID)

	r.fireDue(ctx)

	tasks, err := r.tasks.List(ctx, "web-pentest", 10)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	for _, tk := range tasks {
		if tk.Brief == items[0].Brief {
			t.Fatalf("停用的模板不应展开出 task: %+v", tk)
		}
	}
}
