package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/sandbox"
)

const defaultCommandTimeout = 120

// ─── run_command ─────────────────────────────────────────────────────────────

var runCommandSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command":         {"type": "string", "description": "sh -c 执行的完整 shell 命令（支持管道/重定向/$env）。"},
    "timeout_seconds": {"type": "integer", "description": "最大超时秒数，默认 120。"},
    "tag":             {"type": "string", "description": "日志标签（可选），如 \"sqlmap-l5\"。"}
  },
  "required": ["command"]
}`)

type runCommandTool struct{ deps Deps }

func (t *runCommandTool) Name() string      { return "run_command" }
func (t *runCommandTool) ShortDesc() string { return "在沙箱内执行 shell 命令" }
func (t *runCommandTool) Desc() string {
	return "在沙箱内执行完整 shell 命令（管道/重定向/环境变量），跑外部 CLI 工具。"
}
func (t *runCommandTool) Schema() json.RawMessage { return runCommandSchema }

func (t *runCommandTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Tag            string `json:"tag"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "run_command: 解析参数失败: " + err.Error()}, nil
	}
	if a.Command == "" {
		return registry.ToolResult{Error: "run_command: command 必填"}, nil
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = defaultCommandTimeout
	}

	req := sandbox.ExecRequest{
		TaskID:         t.deps.TaskID,
		AgentID:        t.deps.AgentID,
		Command:        a.Command,
		TimeoutSeconds: a.TimeoutSeconds,
		Tag:            a.Tag,
	}
	res, err := t.deps.Sandbox.Exec(ctx, req)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("run_command: 沙箱执行失败: %v", err)}, nil
	}

	var sb strings.Builder
	if res.Stdout != "" {
		sb.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n--- stderr ---\n")
		}
		sb.WriteString(res.Stderr)
	}
	if res.TimedOut {
		sb.WriteString(fmt.Sprintf("\n[超时: %ds]", a.TimeoutSeconds))
	}
	sb.WriteString(fmt.Sprintf("\n[exit_code: %d]", res.ExitCode))

	output := sb.String()
	return registry.ToolResult{
		Output: output,
		Signal: &registry.Signal{
			Kind:     registry.SignalCmdOutput,
			ToolName: "run_command",
			Content:  truncateOutput(output, 2048),
			Detail:   output,
		},
	}, nil
}

// ─── browser_use ─────────────────────────────────────────────────────────────

var browserUseSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "instruction": {"type": "string", "description": "用自然语言描述要浏览器完成的操作（如：打开登录页、输入用户名密码、点击提交）。"},
    "url":         {"type": "string", "description": "起始 URL（可选）。"},
    "identity":    {"type": "string", "description": "账号名（如 admin），不填则使用当前登录态。"},
    "timeout_seconds": {"type": "integer", "description": "最大超时秒数，默认 120。"}
  },
  "required": ["instruction"]
}`)

type browserUseTool struct{ deps Deps }

func (t *browserUseTool) Name() string      { return "browser_use" }
func (t *browserUseTool) ShortDesc() string { return "用真实浏览器操作目标页面" }
func (t *browserUseTool) Desc() string {
	return "用真实 chromium 浏览器操作目标页面（登录/点击/读 DOM/跑 JS），复用登录态。"
}
func (t *browserUseTool) Schema() json.RawMessage { return browserUseSchema }

func (t *browserUseTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Instruction    string `json:"instruction"`
		URL            string `json:"url"`
		Identity       string `json:"identity"`
		TimeoutSeconds int    `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "browser_use: 解析参数失败: " + err.Error()}, nil
	}
	if a.Instruction == "" {
		return registry.ToolResult{Error: "browser_use: instruction 必填"}, nil
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = defaultCommandTimeout
	}

	// 构造 browser-use 命令，通过沙箱 CLI 执行
	// browser-use 接受自然语言 task，通过 --identity 指定账号
	var cmdParts []string
	cmdParts = append(cmdParts, "browser-use")
	if a.URL != "" {
		cmdParts = append(cmdParts, fmt.Sprintf("--url %q", a.URL))
	}
	if a.Identity != "" {
		cmdParts = append(cmdParts, fmt.Sprintf("--identity %q", a.Identity))
	}
	cmdParts = append(cmdParts, fmt.Sprintf("--task %q", a.Instruction))
	command := strings.Join(cmdParts, " ")

	req := sandbox.ExecRequest{
		TaskID:         t.deps.TaskID,
		AgentID:        t.deps.AgentID,
		Command:        command,
		TimeoutSeconds: a.TimeoutSeconds,
		Tag:            "browser_use",
	}
	res, err := t.deps.Sandbox.Exec(ctx, req)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("browser_use: 沙箱执行失败: %v", err)}, nil
	}

	var sb strings.Builder
	if res.Stdout != "" {
		sb.WriteString(res.Stdout)
	}
	if res.Stderr != "" {
		if sb.Len() > 0 {
			sb.WriteString("\n--- stderr ---\n")
		}
		sb.WriteString(res.Stderr)
	}
	if res.TimedOut {
		sb.WriteString(fmt.Sprintf("\n[超时: %ds]", a.TimeoutSeconds))
	}
	sb.WriteString(fmt.Sprintf("\n[exit_code: %d]", res.ExitCode))

	output := sb.String()
	return registry.ToolResult{
		Output: output,
		Signal: &registry.Signal{
			Kind:     registry.SignalCmdOutput,
			ToolName: "browser_use",
			Content:  truncateOutput(output, 2048),
			Detail:   output,
		},
	}, nil
}

// truncateOutput 截断命令输出以适配 Signal.Content 上限。
func truncateOutput(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n...[truncated]"
}
