package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hibiken/asynq"
)

// Runner 是 agent:run 任务的实际执行者（例如 ReAct 循环）。
//
// 接口放在 worker 包内以避免 handler 反向依赖 runtime；运行时把 runtime.ReAct
// 作为 Runner 注入 handler。接口小、职责单一，便于测试替身。
type Runner interface {
	Run(ctx context.Context, payload AgentRunPayload) error
}

// Handler 将 Asynq 任务解码后交给 Runner 执行。
type Handler struct {
	runner Runner
}

// NewHandler 构造一个绑定具体 Runner 的 Handler。
func NewHandler(runner Runner) *Handler {
	return &Handler{runner: runner}
}

// HandleAgentRun 是注册到 asynq.ServeMux 的 agent:run 处理入口。
//
// 错误处理策略：
//   - payload 解析失败 → asynq.SkipRetry（schema 错误重试也修不好）
//   - Runner 返回错误 → 透传，交由 Asynq 按重试策略处理
func (h *Handler) HandleAgentRun(ctx context.Context, t *asynq.Task) error {
	var payload AgentRunPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("解析 AgentRunPayload 失败: %w: %w", err, asynq.SkipRetry)
	}

	if err := h.runner.Run(ctx, payload); err != nil {
		return fmt.Errorf("runner 执行失败: %w", err)
	}
	return nil
}
