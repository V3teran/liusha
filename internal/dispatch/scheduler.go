// scheduler.go 实现定时下发：轮询 cron_schedule.next_run_at 到点的模板 → 克隆一个新
// assignment（source=direct，schedule_id 指回模板）→ 每个 brief 条目展开一个 task（建会话 +
// enqueue，target_host 由 runner 从 brief 抽取回填）→ 回写 last_run_at/next_run_at。
// 模板条目恒为用户编写的 brief（唯一写入方 DispatchScheduled）。单副本够用；api 多副本时
// 需另抽 leader 选举。

package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/cronschedule"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/worker"
)

// cronPollInterval 是 Scheduler 轮询 cron_schedule 到点模板的周期。
const cronPollInterval = 30 * time.Second

// Scheduler 持有定时下发轮询所需的全部依赖。
type Scheduler struct {
	Schedules     *cronschedule.Store
	Assignments   *assignment.Store
	Tasks         *task.Store
	AgentRuns     *agentrun.Store
	Enq           *worker.Client
	Dispatcher    *Service            // 复用 expandItem（与单发/StartChatTask 同展开逻辑）
	Conversations *conversation.Store // 代理捕获会话流（nil 时 cron 展开 task 不绑会话）
	Logger        zerolog.Logger
}

// Run 阻塞轮询直到 ctx 取消；ctx 取消前再兜最后一轮，避免关停瞬间刚好错过一次到点模板。
func (s *Scheduler) Run(ctx context.Context) {
	s.Logger.Info().Dur("interval", cronPollInterval).Msg("cron scheduler started")
	ticker := time.NewTicker(cronPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.fireDue(ctx)
		}
	}
}

// fireDue 找出全部到点模板并逐个触发；单个模板失败不影响其余模板（记 Warn 继续）。
func (s *Scheduler) fireDue(ctx context.Context) {
	due, err := s.Schedules.ListDue(ctx, time.Now())
	if err != nil {
		s.Logger.Warn().Err(err).Msg("cron ListDue 失败（下轮重试）")
		return
	}
	for _, sched := range due {
		if err = s.fireOne(ctx, sched); err != nil {
			s.Logger.Warn().Err(err).Str("schedule_id", sched.ID).Msg("cron 触发失败（模板保留待下轮重试）")
			continue
		}
	}
}

// fireOne 触发单个到点模板：克隆 assignment → 按 mode 展开每个 item 成 task → 回写 last/next_run_at。
//
// 展开失败的单个 item 不回滚已建的 assignment/其余 item（与 API 单发路径"前面失败直接返错，
// 不留中间状态"不同——这里是批量+定时场景，部分失败不该拖累整批，失败的 item 记 Warn 跳过，
// 模板 next_run_at 仍照常推进，下次到点重新尝试）。
func (s *Scheduler) fireOne(ctx context.Context, sched cronschedule.Schedule) error {
	var items []assignment.Item
	var err error
	if err = json.Unmarshal(sched.Payload, &items); err != nil {
		return fmt.Errorf("unmarshal schedule %s payload: %w", sched.ID, err)
	}

	scheduleID := sched.ID
	// 模板条目是用户编写的目标描述（DispatchScheduled 是模板的唯一写入方），
	// 克隆批次的来源即人工下发。
	asg, err := s.Assignments.Create(ctx, assignment.NewParams{
		Source:      assignment.SourceDirect,
		Items:       items,
		Title:       sched.Title,
		Description: sched.Description,
		ScheduleID:  &scheduleID,
	})
	if err != nil {
		return fmt.Errorf("clone assignment from schedule %s: %w", sched.ID, err)
	}

	for _, item := range items {
		// 与批量下发同一会话语义：每任务建独立会话并落用户 brief 消息（定时任务的执行
		// 现场是可观测资产，哑巴任务只能看结果不能看过程）。expandItem 会链会话 + 落
		// dispatched 节点。
		var expandErr error
		created, cerr := s.Conversations.CreateConversation(ctx, BriefTitle(item.Brief), "")
		if cerr != nil {
			expandErr = fmt.Errorf("create conversation: %w", cerr)
		} else if _, aerr := s.Conversations.AppendMessage(ctx, created.ID, conversation.RoleUser, conversation.KindMessage, item.Brief, nil); aerr != nil {
			expandErr = fmt.Errorf("append user message: %w", aerr)
		} else {
			_, _, expandErr = s.Dispatcher.expandItem(ctx, asg.ID, item.Brief, created.ID, nil, 0)
		}
		if expandErr != nil {
			s.Logger.Warn().Err(expandErr).Str("schedule_id", sched.ID).Str("assignment_id", asg.ID).
				Msg("cron 展开单个 item 失败（跳过，不影响本批其余 item）")
		}
	}

	firedAt := time.Now()
	ok, err := s.Schedules.MarkFired(ctx, sched.ID, firedAt, *sched.NextRunAt)
	if err != nil {
		return fmt.Errorf("mark schedule %s fired: %w", sched.ID, err)
	}
	if !ok {
		// next_run_at 已被其他副本改写：模板被抢先触发，本副本放弃
		s.Logger.Info().Str("schedule_id", sched.ID).Msg("定时模板已被其他副本触发，跳过")
		return nil
	}
	s.Logger.Info().Str("schedule_id", sched.ID).Str("assignment_id", asg.ID).Int("items", len(items)).
		Msg("cron 定时模板已触发")
	return nil
}
