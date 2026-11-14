package dispatch

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/attachment"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/task"
)

// 运行中引导：人在任务运行中修正方向，不停任务。它与生命周期控制（暂停 / 继续 / 停止，
// 决定跑不跑）是两回事，决定的是往哪跑——调整目标作用于目标层，指派与否决作用于动作层。
//
// 每个操作一个事务：锁住任务行、要求任务进行中（暂停中也可以，且保持暂停），写任务行
// 与探索图，连同时间线回执一起提交；提交后才推送回执。智能体只靠读图感知变化（图与
// 任务行是唯一事实源）；运行中的循环另由任务行监视在进程内通知规划者与监察者，让变化
// 更快生效（见 cognition 的 taskWatcher）。

// withdrawReason 是目标调整时撤回未开始的规划动作所记的原因。
const withdrawReason = "目标已调整，撤回未执行的规划"

// AdjustGoal 调整进行中任务的目标：任务行与根目标一同改写（task.SetGoalTx），规划者排好
// 但还没开始的动作撤回为已中止——它们按旧目标排出；用户指派的动作与执行中的动作不动
// （执行中的由监察者按新目标复核）。内容不变什么都不做。任务已结束返回 task.ErrNotActive。
func (s *Service) AdjustGoal(ctx context.Context, taskID, goal string) error {
	return s.steer(ctx, taskID, func(tx pgx.Tx, convID string) (*conversation.Message, error) {
		changed, err := task.SetGoalTx(ctx, tx, taskID, goal)
		if err != nil || !changed {
			return nil, err
		}
		withdrawn, err := explorationgraph.WithdrawPlannedActions(ctx, tx, taskID, withdrawReason)
		if err != nil {
			return nil, err
		}
		return s.receipt(ctx, tx, convID, goal, conversation.SteeringGoalAdjusted, map[string]any{"withdrawn": withdrawn})
	})
}

// AssignAction 在进行中任务的根目标下指派一个动作（来源 user，内容只有指令），返回动作 ID。
// 执行者有空位即按优先级认领；监察者不能否决它。任务已结束返回 task.ErrNotActive。
func (s *Service) AssignAction(ctx context.Context, taskID, instruction string, priority explorationgraph.Priority) (string, error) {
	var actionID string
	err := s.steer(ctx, taskID, func(tx pgx.Tx, convID string) (*conversation.Message, error) {
		if _, err := task.LockActive(ctx, tx, taskID); err != nil {
			return nil, err
		}
		id, err := explorationgraph.AssignAction(ctx, tx, taskID, instruction, priority)
		if err != nil {
			return nil, err
		}
		actionID = id
		return s.receipt(ctx, tx, convID, instruction, conversation.SteeringActionAssigned,
			map[string]any{"action_id": id, "priority": string(priority)})
	})
	return actionID, err
}

// RejectAction 否决进行中任务的一个待执行或执行中的动作（否决方 user）：执行中的动作由
// 执行者的取消开关立即中断，规划者不得重提等效动作。任务已结束返回 task.ErrNotActive；
// 动作已结束返回 explorationgraph.ErrActionSettled；不是本任务的动作返回
// explorationgraph.ErrActionNotFound。
func (s *Service) RejectAction(ctx context.Context, taskID, actionID, reason string) error {
	return s.steer(ctx, taskID, func(tx pgx.Tx, convID string) (*conversation.Message, error) {
		if _, err := task.LockActive(ctx, tx, taskID); err != nil {
			return nil, err
		}
		if err := explorationgraph.RejectActionTx(ctx, tx, taskID, actionID, explorationgraph.SourceUser, reason); err != nil {
			return nil, err
		}
		return s.receipt(ctx, tx, convID, reason, conversation.SteeringActionRejected, map[string]any{"action_id": actionID})
	})
}

// requirementPrefix 标出对话里追加到目标的要求。
const requirementPrefix = "补充要求："

// errGoalTooLong 表示追加后任务目标超过长度契约（task.MaxGoalRunes），本次追加未写入。
var errGoalTooLong = errors.New("追加后任务目标过长")

// appendRequirement 把对话里要执行的消息（连同附件清单）以「补充要求：…」追加到任务目标
// 末尾——与调整目标同一语义（撤回尚未开始的规划动作、回执与变更同事务）：
//   - 任务进行中（含暂停中）：原地追加，暂停保持；运行中的循环由任务行监视感知目标变化；
//   - 任务已结束：同一事务里重开任务并追加，提交后起新一轮 run 从探索图续跑。
func (s *Service) appendRequirement(ctx context.Context, taskID, content string, attachments []attachment.Ref, userMsgSeq int64) error {
	tk, err := s.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return fmt.Errorf("read task: %w", err)
	}
	addition := requirementPrefix + content
	// 先按现有目标预判长度，避免为注定被拒的追加搬动附件；事务内按锁定时的目标终判。
	if len([]rune(tk.Goal))+len([]rune(addition))+2 > task.MaxGoalRunes {
		return errGoalTooLong
	}
	convID, _ := s.Conversations.ResolveConversationByTask(ctx, taskID)
	if len(attachments) > 0 {
		materialized, err := s.materialize(convID, tk.AssignmentID, taskID, attachments)
		if err != nil {
			return err
		}
		addition += s.Attachments.Manifest(materialized)
		s.relinkMessageAttachments(ctx, convID, userMsgSeq, materialized)
	}

	reopened := false
	err = s.steer(ctx, taskID, func(tx pgx.Tx, convID string) (*conversation.Message, error) {
		locked, err := task.Lock(ctx, tx, taskID)
		if err != nil {
			return nil, err
		}
		goal := locked.Goal + "\n\n" + addition
		if len([]rune(goal)) > task.MaxGoalRunes {
			return nil, errGoalTooLong
		}
		if locked.Status != task.StatusActive {
			if err := task.ReopenTx(ctx, tx, taskID); err != nil {
				return nil, err
			}
			reopened = true
		}
		if _, err := task.SetGoalTx(ctx, tx, taskID, goal); err != nil {
			return nil, err
		}
		withdrawn, err := explorationgraph.WithdrawPlannedActions(ctx, tx, taskID, withdrawReason)
		if err != nil {
			return nil, err
		}
		return s.receipt(ctx, tx, convID, addition, conversation.SteeringGoalAppended, map[string]any{"withdrawn": withdrawn})
	})
	if err != nil || !reopened {
		return err
	}
	if _, err := s.enqueuePlannerRun(ctx, taskID, convID); err != nil {
		if !errors.Is(err, agentrun.ErrLiveRunExists) {
			return err
		}
		// 与恢复监控的同 tick 重投竞争（毫秒级窗口）：任务已被拉起，新 run 照样读到追加后的目标。
		chatLog.Info().Str("task", taskID).Msg("续接与并发重投竞争，复用已存活 run")
	}
	if convID != "" {
		s.appendTaskStatus(ctx, convID, conversation.TaskStatusDispatched, "任务已续接，智能体启动中…", nil)
	}
	return nil
}

// steer 在一个事务内执行一次引导写入，提交后推送它的时间线回执（无回执时不推送）。
func (s *Service) steer(ctx context.Context, taskID string, write func(tx pgx.Tx, convID string) (*conversation.Message, error)) error {
	// 会话与任务的关联建好后不变，事务外解析即可。
	convID, err := s.Conversations.ResolveConversationByTask(ctx, taskID)
	if err != nil {
		dispatchLog.Warn().Err(err).Str("task", taskID).Msg("解析任务会话失败（引导照常生效，不写时间线回执）")
		convID = ""
	}
	var msg *conversation.Message
	if err := pgx.BeginFunc(ctx, s.DB, func(tx pgx.Tx) error {
		m, err := write(tx, convID)
		msg = m
		return err
	}); err != nil {
		return fmt.Errorf("任务 %s 引导: %w", taskID, err)
	}
	if msg != nil {
		if err := s.Publisher.PublishMessage(ctx, convID, msg); err != nil {
			dispatchLog.Warn().Err(err).Str("conversation_id", convID).Msg("推送引导回执失败（已落库，刷新可见）")
		}
	}
	return nil
}

// receipt 在事务内写一条引导回执（任务没有会话时不写）。正文是实际写入的内容。
func (s *Service) receipt(ctx context.Context, tx pgx.Tx, convID, content, op string, fields map[string]any) (*conversation.Message, error) {
	if convID == "" {
		return nil, nil
	}
	m, err := conversation.AppendMessageTx(ctx, tx, convID, conversation.RoleUser, conversation.KindEvent,
		content, conversation.SteeringMeta(op, fields))
	if err != nil {
		return nil, err
	}
	return &m, nil
}
