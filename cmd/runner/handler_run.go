package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/conversation"

	"github.com/V3teran/liusha/internal/sandbox"

	"github.com/V3teran/liusha/internal/worker"
)

// ─────────────────────────────────────────────────────────────
//  Tool-invocation recorder (heartbeat + DB telemetry)
// ─────────────────────────────────────────────────────────────

// ─────────────────────────────────────────────────────────────
//  Cognition handler (新架构统一入口)
// ─────────────────────────────────────────────────────────────

// handleCognition 统一的任务执行入口（新架构）。
// 所有任务都走：Planner（6分钟评估） + Executor（5步评估）。
func (h handler) handleCognition(
	ctx context.Context,
	p worker.Payload,
	brief string,
) error {
	if brief == "" {
		return h.failTask(ctx, p.AgentRunID, p.TaskID, p.ConversationID, fmt.Errorf("任务缺少 brief"))
	}

	taskID := p.TaskID
	if err := h.tasks.Heartbeat(ctx, taskID); err != nil {
		h.logger.Warn().Err(err).Str("task_id", taskID).Msg("task入口心跳失败")
	}

	var assignmentID string
	if tk, err := h.tasks.GetByID(ctx, taskID); err == nil {
		assignmentID = tk.AssignmentID
	}
	virtualHost := h.onboard(ctx, assignmentID, taskID, brief)

	// 获取 Sandbox（单例模式：所有任务共享一个容器，通过工作目录隔离）
	sb, err := h.sandboxMgr.Acquire(ctx, sandbox.AcquireRequest{
		TaskID: taskID,
	})
	if err != nil {
		return h.failTask(ctx, p.AgentRunID, taskID, p.ConversationID, fmt.Errorf("sandboxMgr.Acquire(task=%s): %w", taskID, err))
	}
	defer func() {
		if err := h.sandboxMgr.Release(context.Background(), sb); err != nil {
			h.logger.Warn().Err(err).Str("task_id", taskID).Msg("sandboxMgr.Release 失败")
		}
	}()

	h.logger.Info().
		Str("task_id", taskID).
		Str("sandbox_id", sb.ID).
		Str("work_dir", sb.WorkDir).
		Msg("sandbox acquired for task")

	// 执行四Agent认知循环
	report, err := h.runCognition(ctx, taskID, virtualHost, brief, p.ConversationID, sb.Client)
	if err != nil {
		return h.failTask(ctx, p.AgentRunID, taskID, p.ConversationID, err)
	}

	h.logger.Info().
		Str("task_id", taskID).
		Int("steps", report.Steps).
		Int("promoted", report.Promoted).
		Str("stop_why", report.StopWhy).
		Msg("认知循环完成")

	// agent_run 终态回写（成功路径）：认知报告随 result 落库，审计面不再残留
	// running 脏态。独立短超时 ctx——任务 ctx 可能已被上游超时收紧。
	doneCtx, doneCancel := context.WithTimeout(context.Background(), 5*time.Second)
	// run 终态按停止原因分流：外部终止（控制平面 terminate / ctx 取消）落
	// aborted——被杀的 run 标 done 会让审计面误读为自然收敛；认知报告仍随
	// result 落库（终止时也有 steps/attempts 可读）。
	reportJSON, _ := json.Marshal(report)
	terminated := strings.Contains(report.StopWhy, "terminate") || strings.Contains(report.StopWhy, "cancel")
	var runErr error
	if terminated {
		runErr = h.agentRuns.SetAborted(doneCtx, p.AgentRunID)
	} else {
		runErr = h.agentRuns.SetDone(doneCtx, p.AgentRunID, reportJSON)
	}
	if runErr != nil {
		h.logger.Warn().Err(runErr).Str("agent_run_id", p.AgentRunID).Msg("agent_run 终态回写失败（审计面残留 running）")
	}

	// task 成功终态：守卫拒绝（任务已被用户停止/判死）时不覆盖——aborted 节点
	// 由对应写者（api/reaper）负责，此处不落 completed。
	if err := h.tasks.Complete(doneCtx, taskID); err != nil {
		h.logger.Warn().Err(err).Str("task_id", taskID).Msg("task 完成迁移被拒（保持既有终态）")
	} else {
		// 步数口径与时间线卡片一致：一次 agent 推理=一步（dispatched 后的
		// reasoning 终态数）；动作数（detector 的 Action 完成计数）另列字段。
		reasoningSteps := 0
		if p.ConversationID != "" {
			if n, cErr := h.conversations.CountReasoningSinceDispatched(doneCtx, p.ConversationID); cErr != nil {
				h.logger.Warn().Err(cErr).Str("conv", p.ConversationID).Msg("推理步数统计失败（steps 回退动作数口径）")
			} else {
				reasoningSteps = n
			}
		}
		stepsField := reasoningSteps
		if stepsField == 0 {
			stepsField = report.Steps
		}
		h.appendTaskStatus(doneCtx, p.ConversationID, taskID,
			conversation.TaskStatusCompleted, "扫描完成",
			map[string]any{
				"steps":    stepsField,
				"actions":  report.Steps,
				"promoted": report.Promoted,
				"attempts": report.Attempts,
				"stop_why": report.StopWhy,
				"findings": h.findingsCount(doneCtx, taskID),
			})
	}
	doneCancel()
	return nil
}

// appendTaskStatus 落任务生命周期节点并广播 SSE（会话任务专用；无会话纯后台
// 扫描跳过）。best-effort——失败仅告警，不影响状态迁移本体。
func (h handler) appendTaskStatus(ctx context.Context, convID, taskID, status, summary string, fields map[string]any) {
	if convID == "" {
		return
	}
	msg, err := h.conversations.AppendMessage(ctx, convID,
		conversation.RoleAssistant, conversation.KindEvent,
		summary, conversation.TaskStatusMeta(status, fields))
	if err != nil {
		h.logger.Warn().Err(err).Str("conv", convID).Str("status", status).Msg("任务状态节点落库失败")
		return
	}
	if payload, err := json.Marshal(msg); err == nil {
		_ = h.eventPublisher.Publish(ctx, convID, payload)
	}
}

// findingsCount 数 task 黑板 finding（completed 节点报告字段；读失败返回 -1 缺省隐藏）。
func (h handler) findingsCount(ctx context.Context, taskID string) int {
	if h.findings == nil {
		return -1
	}
	fs, err := h.findings.ListByTask(ctx, taskID)
	if err != nil {
		return -1
	}
	return len(fs)
}
