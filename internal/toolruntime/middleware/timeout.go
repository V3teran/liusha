package middleware

import (
	"context"
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/toolruntime"
)

// Timeout 给每个 tool Execute 套一个兜底超时。
//
// 设计动机：read_credentials / read_findings / write_finding / read_memory 等
// 本地工具默认无 timeout——一旦 redis/pg 卡死就把整个 ReAct 拖死。run_command 内部
// 自带 spec.Timeout 钳，但仍依赖 caller 正确设置。本 middleware 在外层兜底，零
// timeout 工具也至少 N 秒退出。
//
// timeoutSeconds <= 0 时退化 no-op（透传 ctx，不加 deadline）。
//
// 配置：cfg.Toolruntime.ToolExecuteTimeoutSeconds（默认 600s）；建议 ≥
// sandbox.run_max_timeout_seconds（300s）让 run_command 不被外层先 cancel。
func Timeout(timeoutSeconds int) toolfx.Middleware {
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
