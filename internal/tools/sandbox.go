package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

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

type runCommandTool struct {
	registry.BaseTool
	deps Deps
}

func newRunCommandTool(deps Deps, timeout time.Duration, safe bool) *runCommandTool {
	t := &runCommandTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

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
		fmt.Fprintf(&sb, "\n[超时: %ds]", a.TimeoutSeconds)
	}
	fmt.Fprintf(&sb, "\n[exit_code: %d]", res.ExitCode)

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

// browserUseSchema 与沙箱内 browser-use CLI 的原子子命令一一对应（open/state/click/...）。
// 驱动方式：open 打开页面 → state 取带元素编号的 DOM → click/input 按编号操作 → state 复查。
var browserUseSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "action": {
      "type": "string",
      "enum": ["open", "state", "click", "input", "type", "select", "hover", "dblclick", "rightclick", "scroll", "back", "keys", "wait", "eval", "get", "screenshot"],
      "description": "原子操作。典型循环：open 打开 → state 取带编号的 DOM 快照 → click/input 按编号交互 → state 复查"
    },
    "url":         {"type": "string", "description": "action=open 时的目标 URL"},
    "index":       {"type": "integer", "description": "元素编号（来自 state 输出），click/input/select/hover/dblclick/rightclick 用"},
    "x":           {"type": "integer", "description": "click 的像素坐标（可选，替代 index）"},
    "y":           {"type": "integer", "description": "click 的像素坐标（可选，替代 index）"},
    "text":        {"type": "string", "description": "input/type 的文本、wait 的 selector/text 条件值"},
    "by":          {"type": "string", "enum": ["selector", "text"], "description": "wait 的条件类型（默认 text）"},
    "timeout_ms":  {"type": "integer", "description": "wait 的超时毫秒（默认 5000）"},
    "keys":        {"type": "string", "description": "action=keys 时按的键（如 Enter、Tab）"},
    "direction":   {"type": "string", "enum": ["up", "down", "left", "right"], "description": "scroll 方向（默认 down）"},
    "amount":      {"type": "integer", "description": "scroll 像素量（默认 500）"},
    "js":          {"type": "string", "description": "action=eval 时执行的 JS 表达式"},
    "get":         {"type": "string", "enum": ["html", "title"], "description": "action=get 取页面内容"},
    "identity":    {"type": "string", "description": "账号身份（独立 cookie jar/chromium），不填用默认"},
    "instruction": {"type": "string", "description": "已废弃：CLI 不支持自然语言 task——改用 action 原子操作组合"}
  },
  "required": ["action"]
}`)

type browserUseTool struct {
	registry.BaseTool
	deps Deps
}

func newBrowserUseTool(deps Deps, timeout time.Duration, safe bool) *browserUseTool {
	t := &browserUseTool{deps: deps}
	t.SetTimeout(timeout)
	t.SetConcurrencySafe(safe)
	return t
}

func (t *browserUseTool) Name() string { return "browser_use" }
func (t *browserUseTool) ShortDesc() string {
	return "用真实浏览器操作目标页面（原子操作）"
}
func (t *browserUseTool) Desc() string {
	return "用真实 chromium 浏览器操作目标页面。open 打开 URL → state 取带元素编号的 DOM 快照 → " +
		"click/input 按编号交互 → state 复查。可 eval JS、get html/title、wait 条件。复用 identity 登录态。"
}
func (t *browserUseTool) Schema() json.RawMessage { return browserUseSchema }

// shellJoin 把子命令参数逐个 shell 引号包裹后拼接（防注入/防空格断词）。
func shellJoin(parts ...string) string {
	quoted := make([]string, 0, len(parts))
	for _, p := range parts {
		if p == "" {
			continue
		}
		quoted = append(quoted, fmt.Sprintf("%q", p))
	}
	return strings.Join(quoted, " ")
}

func (t *browserUseTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Action      string `json:"action"`
		URL         string `json:"url"`
		Index       *int   `json:"index"`
		X           *int   `json:"x"`
		Y           *int   `json:"y"`
		Text        string `json:"text"`
		By          string `json:"by"`
		TimeoutMs   int    `json:"timeout_ms"`
		Keys        string `json:"keys"`
		Direction   string `json:"direction"`
		Amount      *int   `json:"amount"`
		JS          string `json:"js"`
		Get         string `json:"get"`
		Identity    string `json:"identity"`
		Instruction string `json:"instruction"`

		TimeoutSeconds int `json:"timeout_seconds"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "browser_use: 解析参数失败: " + err.Error()}, nil
	}
	if a.Action == "" {
		return registry.ToolResult{Error: "browser_use: action 必填（open/state/click/input/type/select/hover/dblclick/rightclick/scroll/back/keys/wait/eval/get/screenshot）。" +
			"浏览器驱动循环：open URL → state（带编号 DOM）→ click/input 编号 → state 复查"}, nil
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = defaultCommandTimeout
	}
	itoa := func(p *int) string {
		if p == nil {
			return ""
		}
		return strconv.Itoa(*p)
	}

	// 按子命令拼接参数（与 browser-svc.py 的 argv 约定一一对应）
	var argParts string
	switch a.Action {
	case "open":
		if a.URL == "" {
			return registry.ToolResult{Error: "browser_use: open 需要 url"}, nil
		}
		argParts = shellJoin(a.URL)
	case "state", "back", "screenshot":
		// 无参数
	case "click":
		if a.X != nil && a.Y != nil {
			argParts = shellJoin(strconv.Itoa(*a.X), strconv.Itoa(*a.Y))
		} else if a.Index != nil {
			argParts = shellJoin(strconv.Itoa(*a.Index))
		} else {
			return registry.ToolResult{Error: "browser_use: click 需要 index（或 x+y 坐标）"}, nil
		}
	case "input", "select":
		if a.Index == nil || a.Text == "" {
			return registry.ToolResult{Error: "browser_use: " + a.Action + " 需要 index 和 text"}, nil
		}
		argParts = shellJoin(strconv.Itoa(*a.Index), a.Text)
	case "type":
		if a.Text == "" {
			return registry.ToolResult{Error: "browser_use: type 需要 text"}, nil
		}
		argParts = shellJoin(a.Text)
	case "hover", "dblclick", "rightclick":
		if a.Index == nil {
			return registry.ToolResult{Error: "browser_use: " + a.Action + " 需要 index"}, nil
		}
		argParts = shellJoin(strconv.Itoa(*a.Index))
	case "scroll":
		dir := a.Direction
		if dir == "" {
			dir = "down"
		}
		argParts = shellJoin(dir, itoa(a.Amount))
	case "keys":
		if a.Keys == "" {
			return registry.ToolResult{Error: "browser_use: keys 需要 keys（如 Enter）"}, nil
		}
		argParts = shellJoin(a.Keys)
	case "wait":
		if a.Text == "" {
			return registry.ToolResult{Error: "browser_use: wait 需要 text（selector 或 text 条件值）"}, nil
		}
		by := a.By
		if by == "" {
			by = "text"
		}
		tm := a.TimeoutMs
		if tm <= 0 {
			tm = 5000
		}
		argParts = shellJoin(by, a.Text, "--timeout-ms", strconv.Itoa(tm))
	case "eval":
		if a.JS == "" {
			return registry.ToolResult{Error: "browser_use: eval 需要 js 表达式"}, nil
		}
		argParts = shellJoin(a.JS)
	case "get":
		g := a.Get
		if g == "" {
			g = "html"
		}
		argParts = shellJoin(g)
	default:
		return registry.ToolResult{Error: "browser_use: 未知 action " + a.Action}, nil
	}

	// 身份经环境变量注入（IDENTITY=身份 → 独立 cookie jar/chromium）
	command := "browser-use " + a.Action
	if argParts != "" {
		command += " " + argParts
	}
	if a.Identity != "" {
		command = fmt.Sprintf("IDENTITY=%q %s", a.Identity, command)
	}

	execOnce := func(cmd string) (sandbox.ExecResult, error) {
		return t.deps.Sandbox.Exec(ctx, sandbox.ExecRequest{
			TaskID:         t.deps.TaskID,
			AgentID:        t.deps.AgentID,
			Command:        cmd,
			TimeoutSeconds: a.TimeoutSeconds,
			Tag:            "browser_use",
		})
	}
	res, err := execOnce(command)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("browser_use: 沙箱执行失败: %v", err)}, nil
	}

	// 自愈：browser-use 常驻 daemon 跨长任务可能进入退化态（watchdog 超时类内部
	// 错误）——CLI 自带 reset 子命令重建会话。检测到退化签名时 reset 一次并重试
	// 原命令；再失败则原样返回两层输出（诊断上下文保留）。
	if browserDegraded(res) {
		reset, _ := execOnce("browser-use reset")
		retry, rErr := execOnce(command)
		if rErr == nil {
			res = retry
		}
		res.Stdout = fmt.Sprintf("[自愈] 检测到 browser daemon 退化态，已 reset 并重试一次\n--- reset 输出 ---\n%s\n--- 重试输出 ---\n%s", reset.Stdout, res.Stdout)
		res.Stderr = ""
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
		fmt.Fprintf(&sb, "\n[超时: %ds]", a.TimeoutSeconds)
	}
	fmt.Fprintf(&sb, "\n[exit_code: %d]", res.ExitCode)

	output := sb.String()
	// 元素编号失效是浏览器驱动的常态错误（页面跳转/刷新后编号作废）——错误必须
	// 可行动：提示先 state 重建编号映射再操作，否则 LLM 会盲目重试同编号。
	if strings.Contains(output, "not found - page may have changed") {
		output += "\n提示: 元素编号基于最近一次 state 快照，页面变化后即失效。请先执行 {\"action\":\"state\"} 获取最新带编号 DOM，再按新编号重试。"
	}
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

// browserDegraded 识别 browser-use 库的退化态错误签名（watchdog 内部超时等），
// 与站点/操作无关——任何命令在退化 daemon 上都会产出该签名。
func browserDegraded(res sandbox.ExecResult) bool {
	return strings.Contains(res.Stdout, "watchdog") ||
		strings.Contains(res.Stderr, "watchdog") ||
		strings.Contains(res.Stdout, "Event handler browser_use.browser")
}

// truncateOutput 截断命令输出以适配 Signal.Content 上限。
func truncateOutput(s string, limit int) string {
	if len(s) <= limit {
		return s
	}
	return s[:limit] + "\n...[truncated]"
}
