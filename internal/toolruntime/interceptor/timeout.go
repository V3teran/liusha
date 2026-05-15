package interceptor

import (
	"context"
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/toolruntime"
)

// Timeout 给每个 tool Execute 套一个兜底超时。
//
// 设计动机：read_credentials / read_findings / write_finding / read_notes 等
// 本地工具默认无 timeout——一旦 redis/pg 卡死就把整个 ReAct 拖死。run_command 内部
// 自带 spec.Timeout 钳，但仍依赖 caller 正确设置。本 interceptor 在外层兜底，零
// timeout 工具也至少 N 秒退出。
//
// timeoutSeconds <= 0 时退化 no-op（透传 ctx，不加 deadline）。
//
// 配置：cfg.Toolruntime.StepToolTimeoutSeconds（默认 1800s）——单步 tool Execute 上限。
// 同时也是 RunCommand.MaxTimeoutSeconds 钳上限（LLM 传 timeout_seconds 超过即钳到这个值）。
func Timeout(timeoutSeconds int) toolfx.Interceptor {
	if timeoutSeconds <= 0 {
		return func(next toolfx.ActionExecutor) toolfx.ActionExecutor { return next }
	}
	d := time.Duration(timeoutSeconds) * time.Second
	return func(next toolfx.ActionExecutor) toolfx.ActionExecutor {
		return func(ctx context.Context, name string, args json.RawMessage) (toolfx.Result, error) {
			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()
			return next(ctx, name, args)
		}
	}
}
