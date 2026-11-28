package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/sandbox"
)

// defaultCommandTimeout 是沙盒命令未指定超时时的默认秒数（command_run 与 browser_drive 共用）。
const defaultCommandTimeout = 120

// ─── command_run ─────────────────────────────────────────────────────────────

var runCommandSchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "command":         {"type": "string", "description": "sh -c 执行的完整 shell 命令（支持管道/重定向/$env）。"},
    "timeout_seconds": {"type": "integer", "description": "最大超时秒数，默认 120。"},
    "tag":             {"type": "string", "description": "日志标签（可选）。"}
  },
  "required": ["command"]
}`)

type runCommandTool struct {
	registry.BaseTool
	deps Deps
}

// newRunCommandTool 构造 command_run。超时钳制上限与工具结果窗口都来自 runtime 配置（出厂值
// 在 config.yaml，热改值在系统设置），装配处必须给出——工具不再内置出厂值的第二份：配置改了
// 它不会跟着变，留着只会让人以为「没配也行」。
func newRunCommandTool(deps Deps, timeout time.Duration) *runCommandTool {
	if deps.StepToolTimeoutSeconds <= 0 || deps.OutputWindowBytes <= 0 || timeout <= 0 {
		panic("command_run 需要 runtime.step_tool_timeout_seconds 与 runtime.tool_output_window_bytes（均须为正，注册表级超时同源）：装配处未给出配置值")
	}
	t := &runCommandTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *runCommandTool) Name() string      { return "command_run" }
func (t *runCommandTool) ShortDesc() string { return "在沙箱内执行 shell 命令" }
func (t *runCommandTool) Desc() string {
	desc := "在沙箱内执行完整 shell 命令（管道/重定向/环境变量），跑外部 CLI 工具。命令以 root 身份执行，sudo 不可用也无必要；预装清单外的命令也能写，但未安装的会报 command not found。" +
		" 输出很长时你看到的只是开头和结尾，结果末尾写着总字节数和完整输出的文件路径：需要中间的内容就用 sed -n、grep 分段读那个文件，不要重跑命令。"
	// 对外可达性契约：不告知时 LLM 会随手挑端口起监听（端口未对外发布时目标连
	// 不进来，回连静默失败）。提供监听段时显式告知可用端口段与回连地址，让需回连
	// 的命令一次写对；未提供时同样明示，阻止注定
	// 失败的回连尝试。
	if t.deps.ListenerPortCount > 0 && t.deps.ListenerPortStart > 0 {
		end := t.deps.ListenerPortStart + t.deps.ListenerPortCount - 1
		if t.deps.CallbackHost != "" {
			desc += fmt.Sprintf(" 需目标回连的监听只绑 %d-%d 端口（目标经回连地址的同号端口直达），回连地址 %s；其它端口不保证目标可达。长驻监听后台运行并重定向输出（如 cmd > /tmp/listen.log 2>&1 &）以免阻塞本步；端口被并行任务占用时换段内下一个。",
				t.deps.ListenerPortStart, end, t.deps.CallbackHost)
		} else {
			desc += fmt.Sprintf(" 需目标回连的监听只绑 %d-%d 端口（目标经回连地址的同号端口直达）；回连地址未配置（sandbox.callback_host），向操作员确认后再构造需回连的命令。",
				t.deps.ListenerPortStart, end)
		}
	} else {
		desc += " 沙盒未发布对外监听端口：需目标主动回连的操作不可达，改用出站型方式（由本机主动连目标、再拉取结果）。"
	}
	return desc
}
func (t *runCommandTool) Schema() json.RawMessage { return runCommandSchema }

func (t *runCommandTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var a struct {
		Command        string `json:"command"`
		TimeoutSeconds int    `json:"timeout_seconds"`
		Tag            string `json:"tag"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return registry.ToolResult{Error: "command_run: 解析参数失败: " + err.Error()}, nil
	}
	if a.Command == "" {
		return registry.ToolResult{Error: "command_run: command 必填"}, nil
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = defaultCommandTimeout
	}
	// 钳制上界 = runtime.step_tool_timeout_seconds（系统配置，注册表级超时同源）——
	// 防本地工具卡死；LLM 自传更长的 timeout 会被截到上界。
	if ceiling := runCommandTimeout(t.deps); a.TimeoutSeconds > int(ceiling.Seconds()) {
		a.TimeoutSeconds = int(ceiling.Seconds())
	}

	// 落盘阈值取工具结果的窗口：输出超过窗口就会被窗口省掉中间，省掉的部分得有处可读。
	req := sandbox.ExecRequest{
		AssignmentID:        t.deps.AssignmentID,
		TaskID:              t.deps.TaskID,
		AgentRunID:          t.deps.AgentRunID,
		Command:             a.Command,
		TimeoutSeconds:      a.TimeoutSeconds,
		Tag:                 a.Tag,
		SaveOutputOverBytes: t.deps.OutputWindowBytes,
	}
	res, err := t.deps.Sandbox.Exec(ctx, req)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("command_run: 沙箱执行失败: %v", err)}, nil
	}

	// 输出原样交出，这里不裁剪：工具结果进上下文之前只有运行时的那一道窗口（留开头与结尾、
	// 中间写明省掉多少）。此前这里先只留尾部 8 KiB——扫描类工具的结果按序排在中间，模型还没
	// 看到就先被扔掉了前段，nmap 的低端口恰好在最前面。
	var sb strings.Builder
	sb.WriteString(res.Stdout)
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

	// 以下几行都在结果末尾：无论窗口怎么切，末尾都保留——模型看得到输出完不完整、缺的在哪儿。
	// 完整输出已由沙盒落盘的流，写明总字节数与文件路径（命令可以分段读它）。
	if res.StdoutFile != "" {
		fmt.Fprintf(&sb, "\n[stdout 共 %d 字节；完整输出: %s]", res.StdoutBytes, res.StdoutFile)
	}
	if res.StderrFile != "" {
		fmt.Fprintf(&sb, "\n[stderr 共 %d 字节；完整输出: %s]", res.StderrBytes, res.StderrFile)
	}
	for _, warning := range res.Warnings {
		fmt.Fprintf(&sb, "\n[沙盒提示: %s]", warning)
	}

	// $OUTPUT_DIR 新增产物随结果附清单——文件本体留在 out/（宿主持久层，
	// 操作员工作空间页可见可下载），清单进 LLM 上下文使其知道「我产出过
	// 什么文件」，后续可 cat 复核或写进观测。此前收集结果被整个丢弃，
	// agent 无从知晓产物通道的存在。
	if len(res.Files) > 0 {
		fmt.Fprintf(&sb, "\n[out/ 产物 %d 个:", len(res.Files))
		for i, f := range res.Files {
			if i > 0 {
				sb.WriteString(",")
			}
			fmt.Fprintf(&sb, " %s(%dKB)", f.Name, len(f.B64)*3/4/1024)
		}
		sb.WriteString(" —— 已存档，操作员可在工作空间页查看]")
	}

	return registry.ToolResult{Output: sb.String()}, nil
}

// runCommandTimeout 返回 command_run 的注册表级超时 = runtime.step_tool_timeout_seconds
// （配置里的出厂值或页面上的热改值，任务开始时快照）。它与 Execute 内的钳制上界同源——
// 此前注册表硬编码 120s 而配置写 1800s，LLM 自传长超时实际仍被 120s 掐死。构造时已校验
// 它为正。
func runCommandTimeout(deps Deps) time.Duration {
	return time.Duration(deps.StepToolTimeoutSeconds) * time.Second
}
