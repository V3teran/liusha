package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// ─────────────────────────────────────────────
//  Shell 命令工具
// ─────────────────────────────────────────────

// ShellTool Shell 命令执行工具
type ShellTool struct {
	timeout       time.Duration
	allowedCmds   []string // 允许的命令白名单（为空则不限制）
	forbiddenCmds []string // 禁止的命令黑名单
}

// NewShellTool 创建 Shell 工具
func NewShellTool(timeout time.Duration) *ShellTool {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &ShellTool{
		timeout: timeout,
		forbiddenCmds: []string{
			"rm -rf /",
			":(){ :|:& };:", // fork bomb
			"mkfs",
			"dd if=/dev/zero",
		},
	}
}

// WithAllowedCommands 设置命令白名单
func (t *ShellTool) WithAllowedCommands(cmds []string) *ShellTool {
	t.allowedCmds = cmds
	return t
}

// WithForbiddenCommands 设置命令黑名单
func (t *ShellTool) WithForbiddenCommands(cmds []string) *ShellTool {
	t.forbiddenCmds = cmds
	return t
}

// Name 工具名称
func (t *ShellTool) Name() string {
	return "shell"
}

// Description 工具描述
func (t *ShellTool) Description() string {
	return "执行 Shell 命令"
}

// Parameters 参数 Schema
func (t *ShellTool) Parameters() json.RawMessage {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "要执行的命令",
			},
			"working_dir": map[string]any{
				"type":        "string",
				"description": "工作目录（可选）",
			},
		},
		"required": []string{"command"},
	}
	data, _ := json.Marshal(schema)
	return data
}

// Execute 执行工具
func (t *ShellTool) Execute(ctx context.Context, input string) (string, error) {
	// 解析输入
	var params struct {
		Command    string `json:"command"`
		WorkingDir string `json:"working_dir"`
	}

	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("参数解析失败: %w", err)
	}

	if params.Command == "" {
		return "", fmt.Errorf("命令不能为空")
	}

	// 安全检查
	if err := t.checkSafety(params.Command); err != nil {
		return "", err
	}

	// 创建带超时的上下文
	cmdCtx, cancel := context.WithTimeout(ctx, t.timeout)
	defer cancel()

	// 执行命令
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", params.Command)
	if params.WorkingDir != "" {
		cmd.Dir = params.WorkingDir
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	// 构建输出
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	if err != nil {
		return output, fmt.Errorf("命令执行失败: %w", err)
	}

	return output, nil
}

// checkSafety 检查命令安全性
func (t *ShellTool) checkSafety(command string) error {
	// 检查黑名单
	for _, forbidden := range t.forbiddenCmds {
		if strings.Contains(command, forbidden) {
			return fmt.Errorf("禁止执行危险命令: %s", forbidden)
		}
	}

	// 检查白名单
	if len(t.allowedCmds) > 0 {
		allowed := false
		for _, allowedCmd := range t.allowedCmds {
			if strings.HasPrefix(strings.TrimSpace(command), allowedCmd) {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("命令不在白名单中")
		}
	}

	return nil
}
