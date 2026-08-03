// scheduler.go 实现定时下发（spec §4.2 + §10 P4）：轮询 cron_schedule.next_run_at 到点的
// 模板 → 克隆一个新 assignment（source=auto，schedule_id 指回模板）→ 展开子 task →
// 回写 last_run_at/next_run_at。单副本够用；api 多副本时需另抽 leader 选举，见 spec §4.2。
//
// 展开只按 item 形态派发输入供给（不关心引擎——引擎由 runner 按 task.scenario_id 解析，数据驱动）：
//   - item.Host 非空 → 流量复检：建 task（brief=host）+ 领取该 host 未消费 proxy_traffic + enqueue。
//   - 否则 → brief 扫描：建 task（brief 原文）+ enqueue，target_host 由 runner 从 brief 抽取回填。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/hunterrun"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/worker"
)

// cronPollInterval 是 Scheduler 轮询 cron_schedule 到点模板的周期。
const cronPollInterval = 30 * time.Second

// passiveCronClaimLimit 是定时触发 passive 展开时一次性领取的未消费 proxy_traffic 上限——
// 定时重触发是低频操作（非实时聚合窗口），给一个宽松固定值即可，无需接聚合器配置。
const passiveCronClaimLimit = 200

// cronRunner 持有 Scheduler 轮询所需的全部依赖。
type cronRunner struct {
	schedules   *cronschedule.Store
	assignments *assignment.Store
	tasks       *task.Store
	proxyFlows  *traffic.ProxyStore
	hunters     *hunterrun.Store
	enq         *worker.Client
	scan        *scanAdapter // 复用 expandItem（与单发/StartChatScan 同展开逻辑）
	logger      zerolog.Logger
}

// run 阻塞轮询直到 ctx 取消；ctx 取消前再兜最后一轮，避免关停瞬间刚好错过一次到点模板。
func (r *cronRunner) run(ctx context.Context) {
	r.logger.Info().Dur("interval", cronPollInterval).Msg("cron scheduler started")
	ticker := time.NewTicker(cronPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.fireDue(ctx)
		}
	}
}

// fireDue 找出全部到点模板并逐个触发；单个模板失败不影响其余模板（记 Warn 继续）。
func (r *cronRunner) fireDue(ctx context.Context) {
	due, err := r.schedules.ListDue(ctx, time.Now())
	if err != nil {
		r.logger.Warn().Err(err).Msg("cron ListDue 失败（下轮重试）")
		return
	}
	for _, sched := range due {
		if err := r.fireOne(ctx, sched); err != nil {
			r.logger.Warn().Err(err).Str("schedule_id", sched.ID).Msg("cron 触发失败（模板保留待下轮重试）")
			continue
		}
	}
}

// fireOne 触发单个到点模板：克隆 assignment → 按 mode 展开每个 item 成 task → 回写 last/next_run_at。
//
// 展开失败的单个 item 不回滚已建的 assignment/其余 item（与 API 单发路径"前面失败直接返错，
// 不留中间状态"不同——这里是批量+定时场景，部分失败不该拖累整批，失败的 item 记 Warn 跳过，
// 模板 next_run_at 仍照常推进，下次到点重新尝试）。
func (r *cronRunner) fireOne(ctx context.Context, sched cronschedule.CronSchedule) error {
	var items []assignment.Item
	if err := json.Unmarshal(sched.Payload, &items); err != nil {
		return fmt.Errorf("unmarshal schedule %s payload: %w", sched.ID, err)
	}

	scheduleID := sched.ID
	asg, err := r.assignments.Create(ctx, assignment.NewParams{
		ScenarioID: sched.ScenarioID,
		Source:     assignment.SourceAuto,
		Items:      items,
		Title:      sched.Title,
		ScheduleID: &scheduleID,
	})
	if err != nil {
		return fmt.Errorf("clone assignment from schedule %s: %w", sched.ID, err)
	}

	for _, item := range items {
		var expandErr error
		if item.Host != "" {
			expandErr = r.expandTrafficItem(ctx, asg.ID, sched.ScenarioID, item)
		} else {
			_, _, expandErr = r.scan.expandItem(ctx, asg.ID, item.Brief, "", sched.ScenarioID)
		}
		if expandErr != nil {
			r.logger.Warn().Err(expandErr).Str("schedule_id", sched.ID).Str("assignment_id", asg.ID).
				Msg("cron 展开单个 item 失败（跳过，不影响本批其余 item）")
		}
	}

	if err := r.schedules.MarkFired(ctx, sched.ID, time.Now()); err != nil {
		return fmt.Errorf("mark schedule %s fired: %w", sched.ID, err)
	}
	r.logger.Info().Str("schedule_id", sched.ID).Str("assignment_id", asg.ID).Int("items", len(items)).
		Msg("cron 定时模板已触发")
	return nil
}

// expandTrafficItem 把 assignment 下的一个流量复检 item（host）展开成 task + 领取该 host
// 未消费的 proxy_traffic + enqueue——与 ingestor.spawnPassiveTask 同语义，区别仅在触发源是
// 定时器而非实时流量窗口（故用固定 passiveCronClaimLimit，不接聚合器配置）。
//
// brief 存 host（统一输入，见 D5）；引擎由 runner 按 scenarioID 解析（此处不关心 solo/swarm）。
func (r *cronRunner) expandTrafficItem(ctx context.Context, assignmentID, scenarioID string, item assignment.Item) error {
	host := item.Host
	tk, err := r.tasks.Create(ctx, task.NewParams{ScenarioID: scenarioID, AssignmentID: assignmentID, Brief: host, TargetHost: host})
	if err != nil {
		return fmt.Errorf("create task: %w", err)
	}

	claimed, err := r.proxyFlows.ClaimUnconsumedByHost(ctx, tk.ID, host, passiveCronClaimLimit)
	if err != nil {
		_ = r.tasks.Abort(ctx, tk.ID, "领取流量失败")
		return fmt.Errorf("claim unconsumed proxy_traffic for host %s: %w", host, err)
	}
	if claimed == 0 {
		_ = r.tasks.Abort(ctx, tk.ID, "无未消费流量可分析")
		return nil // 不算错误：到点但该 host 当前无未消费流量，正常空转
	}

	payloadInput, _ := json.Marshal(map[string]string{"brief": host})
	hid, err := r.hunters.Create(ctx, hunterrun.NewParams{
		TaskID: tk.ID,
		Role:   "orchestrator",
		Input:  payloadInput,
	})
	if err != nil {
		return fmt.Errorf("create hunter run: %w", err)
	}
	if _, _, err := r.enq.Enqueue(ctx, worker.RoleHunter, worker.Payload{
		HunterID:   hid,
		TaskID:     tk.ID,
		ScenarioID: scenarioID,
		Input:      payloadInput,
		Role:       worker.RoleHunter,
	}); err != nil {
		return fmt.Errorf("enqueue: %w", err)
	}
	return nil
}
