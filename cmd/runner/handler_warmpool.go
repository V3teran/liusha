package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/worker"
	"github.com/rs/zerolog"
)

// warmPoolHandler 是热池模式的极简 handler（完全跳过数据库，只负责 sandbox 执行）
type warmPoolHandler struct {
	sandboxMgr sandbox.Manager
	logger     zerolog.Logger
}

func newWarmPoolHandler(sandboxMgr sandbox.Manager, logger zerolog.Logger) *warmPoolHandler {
	return &warmPoolHandler{
		sandboxMgr: sandboxMgr,
		logger:     logger,
	}
}

func (h *warmPoolHandler) handle(ctx context.Context, p worker.Payload) error {
	h.logger.Info().
		Str("agent_id", p.AgentID).
		Str("task_id", p.TaskID).
		Msg("🚀 warm pool handler: task started")

	// 1. Acquire sandbox
	sb, err := h.sandboxMgr.Acquire(ctx, sandbox.AcquireRequest{
		TaskID: p.TaskID,
	})
	if err != nil {
		return fmt.Errorf("acquire sandbox: %w", err)
	}
	defer func() {
		if relErr := h.sandboxMgr.Release(ctx, sb); relErr != nil {
			h.logger.Warn().Err(relErr).Msg("release sandbox failed")
		}
	}()

	h.logger.Info().
		Str("sandbox_id", sb.ID).
		Str("task_id", p.TaskID).
		Msg("✅ sandbox acquired")

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
		command = fmt.Sprintf("echo '=== Warm Pool Test ===' && echo 'Brief: %s' && pwd && ls -la && echo '✅ Test completed'", input.Brief)
	}
	if command == "" {
		command = "echo '=== Warm Pool Sandbox ===' && pwd && uname -a && echo '✅ Ready'"
	}

	// 4. 执行命令
	result, err := sb.Client.Exec(ctx, sandbox.ExecRequest{
		TaskID:         p.TaskID,
		AgentID:        p.AgentID,
		Command:        command,
		TimeoutSeconds: 30,
	})
	if err != nil {
		return fmt.Errorf("exec command: %w", err)
	}

	h.logger.Info().
		Int("exit_code", result.ExitCode).
		Str("stdout", result.Stdout).
		Str("stderr", result.Stderr).
		Msg("✅ command executed")

	return nil
}
