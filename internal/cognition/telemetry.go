// telemetry.go 工具调用遥测：每次工具调用落 tool_invocation、活动打点（执行者看门狗判动作停摆的
// 依据），并把工具的超时登记到所属 run 的存活心跳（恢复监控判 run 是否还在被推进的依据）。

package cognition

import (
	"context"
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools"
)

// toolRecordInterceptor returns a registry.Interceptor that records every tool
// call to toolinvocation.Store and touches the action activity tracker.
func (d *Deps) toolRecordInterceptor(agentRunID, taskID string, activity *executor.ActivityTracker) registry.Interceptor {
	return func(ctx context.Context, t registry.Tool, args []byte, next registry.ExecuteFunc) (registry.ToolResult, error) {
		d.Logger.Debug().
			Str("tool_name", t.Name()).
			Msg("tool call intercepted")

		// 工具在途期间 run 的存活心跳照常续约：超时拦截器在链的外侧，这里的 ctx 带着工具的超时。
		if d.RunActivity != nil {
			until, _ := ctx.Deadline()
			defer d.RunActivity.Busy(agentRunID, until)()
		}

		start := time.Now()
		res, err := next(ctx, t, args)
		durMs := int(time.Since(start).Milliseconds())

		errMsg := ""
		if res.Error != "" {
			errMsg = res.Error
		} else if err != nil {
			errMsg = err.Error()
		}
		// record tool invocation (best-effort)；role 取 ctx 的 agent 身份
		// （lifecycle 启动时注入）——跨 agent 行为审计的归属列。
		if d.ToolCalls != nil {
			invID, appendErr := d.ToolCalls.Append(ctx, toolinvocation.Invocation{
				AgentRunID:   agentRunID,
				TaskID:       taskID,
				Role:         AgentFromContext(ctx),
				ToolName:     t.Name(),
				Args:         json.RawMessage(args),
				OutputSize:   len(res.Output),
				Output:       res.Output,
				DurationMs:   durMs,
				ErrorMessage: errMsg,
			})
			if appendErr != nil {
				logx.ErrorUnlessCanceled(d.Logger, appendErr).Err(appendErr).
					Str("tool_name", t.Name()).
					Msg("failed to record tool invocation")
			} else {
				d.Logger.Debug().
					Str("tool_name", t.Name()).
					Int64("invocation_id", invID).
					Msg("tool invocation recorded")
			}
		} else {
			d.Logger.Warn().
				Str("tool_name", t.Name()).
				Msg("d.ToolCalls is nil, cannot record invocation")
		}
		// 进展打点：执行者看门狗的停摆判据（图上无输出 ≠ 卡死）
		activity.Touch(tools.ActionIDFromContext(ctx))
		return res, err
	}
}
