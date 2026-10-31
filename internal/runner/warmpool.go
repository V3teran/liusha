package runner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/rs/zerolog"
)

// WarmPoolHandler 是热池模式的极简 handler（完全跳过数据库，只负责 sandbox 执行）
type WarmPoolHandler struct {
	SandboxMgr sandbox.Manager
	Logger     zerolog.Logger
}

// NewWarmPoolHandler 构造冒烟 handler。
func NewWarmPoolHandler(sandboxMgr sandbox.Manager, logger zerolog.Logger) *WarmPoolHandler {
	return &WarmPoolHandler{
		SandboxMgr: sandboxMgr,
		Logger:     logger,
	}
}

// Handle 处理单次冒烟任务：领沙箱 → 执行 echo/命令 → 释放。不写 DB、不跑认知循环。
func (h *WarmPoolHandler) Handle(ctx context.Context, p worker.Payload) error {
	h.Logger.Info().
		Str("agent_run_id", p.AgentRunID).
		Str("task_id", p.TaskID).
		Msg("warm pool handler: task started")

	// 1. Acquire sandbox
	sb, err := h.SandboxMgr.Acquire(ctx, sandbox.AcquireRequest{
		TaskID: p.TaskID,
	})
	if err != nil {
		return fmt.Errorf("acquire sandbox: %w", err)
	}
	defer func() {
		if relErr := h.SandboxMgr.Release(ctx, sb); relErr != nil {
			h.Logger.Warn().Err(relErr).Msg("release sandbox failed")
		}
	}()

	h.Logger.Info().
		Str("sandbox_id", sb.ID).
		Str("task_id", p.TaskID).
		Msg("sandbox acquired")

	// 2. 解析输入
	var input struct {
		Command string `json:"command"`
		Brief   string `json:"brief"`
	}
	if len(p.Input) > 0 {
		if err := json.Unmarshal(p.Input, &input); err != nil {
			return fmt.Errorf("unmarshal input: %w", err)
		}
	}

	// 3. 构造命令
	command := input.Command
	if command == "" && input.Brief != "" {
		command = fmt.Sprintf("echo '=== Warm Pool Test ===' && echo 'Brief: %s' && pwd && ls -la && echo 'Test completed'", input.Brief)
	}
	if command == "" {
		command = "echo '=== Warm Pool Sandbox ===' && pwd && uname -a && echo 'Ready'"
	}

	// 4. 执行命令
	result, err := sb.Client.Exec(ctx, sandbox.ExecRequest{
		TaskID:         p.TaskID,
		AgentID:        p.AgentRunID,
		Command:        command,
		TimeoutSeconds: 30,
	})
	if err != nil {
		return fmt.Errorf("exec command: %w", err)
	}

	h.Logger.Info().
		Int("exit_code", result.ExitCode).
		Str("stdout", result.Stdout).
		Str("stderr", result.Stderr).
		Msg("command executed")

	return nil
}
