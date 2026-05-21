package interceptor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/toolinvocation"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
)

// recordLog 包级 logger（与 observe/timeout 同模式）。
var recordLog = logx.New("toolruntime.record")

// Record 把每次 tool Execute 持久化为 tool_invocation 行（telemetry）。
//
// 参数 agentTaskID / ownerType / ownerID 是 registry-scope（per agent_task）：
// hunter/skill.go NewBuilder 每次构造 reg 时把当下 task 的标识捕获进闭包。
//
// 写入 best-effort：Append 失败仅 log warn 不影响业务返回（Execute 仍正常完成）。
// 用 context.Background 与 ReAct ctx 解耦——避免 ReAct cancel 后 telemetry 写入也中断。
func Record(store *toolinvocation.Store, agentTaskID, ownerType, ownerID string) toolfx.Interceptor {
	return func(next toolfx.ActionExecutor) toolfx.ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (toolfx.Result, error) {
			start := time.Now()
			res, err := next(ctx, name, args)
			dur := time.Since(start)

			if store == nil || agentTaskID == "" || ownerID == "" {
				// 未注入 store 或 task 上下文缺失，跳过 telemetry（保留 observe 路径）。
				return res, err
			}

			errMsg := ""
			if err != nil {
				errMsg = err.Error()
			}
			inv := toolinvocation.Invocation{
				AgentTaskID:   agentTaskID,
				OwnerType:     ownerType,
				OwnerID:       ownerID,
				ToolName:      name,
				Args:          args,
				OutputSize:    len(res.Output),
				OutputPreview: string(res.Output),
				DurationMs:    int(dur.Milliseconds()),
				ErrorMessage:  errMsg,
				Done:          res.Done,
			}
			if _, appendErr := store.Append(context.Background(), inv); appendErr != nil {
				recordLog.Warn().
					Err(appendErr).
					Str("tool", name).
					Str("agent_task_id", agentTaskID).
					Msg("tool_invocation 记录失败（不阻塞业务）")
			}
			return res, err
		}
	}
}
