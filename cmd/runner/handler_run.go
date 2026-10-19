package main

import (
	"context"
	"fmt"

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
		return h.failTask(ctx, p.AgentRunID, fmt.Errorf("任务缺少 brief"))
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
		return h.failTask(ctx, p.AgentRunID, fmt.Errorf("sandboxMgr.Acquire(task=%s): %w", taskID, err))
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
		return h.failTask(ctx, p.AgentRunID, err)
	}

	h.logger.Info().
		Str("task_id", taskID).
		Int("steps", report.Steps).
		Int("promoted", report.Promoted).
		Str("stop_why", report.StopWhy).
		Msg("认知循环完成")

	// 任务完成
	if err := h.tasks.Complete(ctx, taskID); err != nil {
		h.logger.Error().Err(err).Str("task_id", taskID).Msg("failed to mark task complete")
	}
	return nil
}
