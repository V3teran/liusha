package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/worker"
)

// const
// heartbeatThrottleMs: minimum gap between consecutive heartbeat writes.
const heartbeatThrottleMs = 10_000

// ─────────────────────────────────────────────────────────────
//  Tool-invocation recorder (heartbeat + DB telemetry)
// ─────────────────────────────────────────────────────────────

// toolRecordInterceptor returns a registry.Interceptor that records every tool
// call to toolinvocation.Store and throttles task heartbeats.
func (h handler) toolRecordInterceptor(agentRunID, taskID string) registry.Interceptor {
	lastBeatMs := new(atomic.Int64)
	return func(ctx context.Context, t registry.Tool, args []byte, next registry.ExecuteFunc) (registry.ToolResult, error) {
		h.logger.Debug().
			Str("task_id", taskID).
			Str("tool_name", t.Name()).
			Msg("tool call intercepted")

		start := time.Now()
		res, err := next(ctx, t, args)
		durMs := int(time.Since(start).Milliseconds())

		errMsg := ""
		if res.Error != "" {
			errMsg = res.Error
		} else if err != nil {
			errMsg = err.Error()
		}
		// record tool invocation (best-effort)
		if h.toolCalls != nil {
			preview := res.Output
			if len(preview) > 512 {
				preview = preview[:512]
			}
			invID, appendErr := h.toolCalls.Append(ctx, toolinvocation.Invocation{
				AgentRunID:    agentRunID,
				TaskID:        taskID,
				ToolName:      t.Name(),
				Args:          json.RawMessage(args),
				OutputSize:    len(res.Output),
				OutputPreview: preview,
				DurationMs:    durMs,
				ErrorMessage:  errMsg,
			})
			if appendErr != nil {
				h.logger.Error().Err(appendErr).
					Str("task_id", taskID).
					Str("tool_name", t.Name()).
					Msg("failed to record tool invocation")
			} else {
				h.logger.Debug().
					Str("task_id", taskID).
					Str("tool_name", t.Name()).
					Int64("invocation_id", invID).
					Msg("tool invocation recorded")
			}
		} else {
			h.logger.Warn().
				Str("task_id", taskID).
				Str("tool_name", t.Name()).
				Msg("h.toolCalls is nil, cannot record invocation")
		}
		// throttled heartbeat
		if taskID != "" {
			now := time.Now().UnixMilli()
			last := lastBeatMs.Load()
			if now-last >= heartbeatThrottleMs {
				if lastBeatMs.CompareAndSwap(last, now) {
					hctx, hcancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer hcancel()
					if h.tasks != nil {
						_ = h.tasks.Heartbeat(hctx, taskID)
					}
				}
			}
		}
		return res, err
	}
}

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
	report, err := h.runCognition(ctx, taskID, virtualHost, brief, sb.Client)
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
