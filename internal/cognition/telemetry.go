// telemetry.go 工具调用遥测：每次工具调用落 tool_invocation、节流续命
// task.heartbeat_at（reaper 判活依据）、活动打点（monitor kill 判据）。

package cognition

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/toolinvocation"
)

// heartbeatThrottleMs 是连续两次心跳写入的最小间隔。
const heartbeatThrottleMs = 10_000

// toolRecordInterceptor returns a registry.Interceptor that records every tool
// call to toolinvocation.Store and throttles task heartbeats.
func (d *Deps) toolRecordInterceptor(agentRunID, taskID string, activities *activityTracker) registry.Interceptor {
	lastBeatMs := new(atomic.Int64)
	return func(ctx context.Context, t registry.Tool, args []byte, next registry.ExecuteFunc) (registry.ToolResult, error) {
		d.Logger.Debug().
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
		if d.ToolCalls != nil {
			preview := res.Output
			if len(preview) > 512 {
				preview = preview[:512]
			}
			invID, appendErr := d.ToolCalls.Append(ctx, toolinvocation.Invocation{
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
				d.Logger.Error().Err(appendErr).
					Str("task_id", taskID).
					Str("tool_name", t.Name()).
					Msg("failed to record tool invocation")
			} else {
				d.Logger.Debug().
					Str("task_id", taskID).
					Str("tool_name", t.Name()).
					Int64("invocation_id", invID).
					Msg("tool invocation recorded")
			}
		} else {
			d.Logger.Warn().
				Str("task_id", taskID).
				Str("tool_name", t.Name()).
				Msg("d.ToolCalls is nil, cannot record invocation")
		}
		// 活动打点：monitor 的 kill 判据依据（图上无输出 ≠ 卡死）
		if activities != nil {
			activities.touch(ctx)
		}

		// throttled heartbeat
		if taskID != "" {
			now := time.Now().UnixMilli()
			last := lastBeatMs.Load()
			if now-last >= heartbeatThrottleMs {
				if lastBeatMs.CompareAndSwap(last, now) {
					hctx, hcancel := context.WithTimeout(context.Background(), 3*time.Second)
					defer hcancel()
					if d.Tasks != nil {
						_ = d.Tasks.Heartbeat(hctx, taskID)
					}
				}
			}
		}
		return res, err
	}
}
