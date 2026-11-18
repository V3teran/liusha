package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/sandbox"
)

// defaultCommandTimeout 是沙盒命令未指定超时时的默认秒数（run_command 与 drive_browser 共用）。
const defaultCommandTimeout = 120

// defaultRunTailBytes 是 run_command 输出尾部保留的默认字节数（runtime.run_tail_bytes 未配置时）。
const defaultRunTailBytes = 8192

// ─── run_command ─────────────────────────────────────────────────────────────

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

func newRunCommandTool(deps Deps, timeout time.Duration) *runCommandTool {
	t := &runCommandTool{deps: deps}
	t.SetTimeout(timeout)
	return t
}

func (t *runCommandTool) Name() string      { return "run_command" }
func (t *runCommandTool) ShortDesc() string { return "在沙箱内执行 shell 命令" }
func (t *runCommandTool) Desc() string {
	desc := "在沙箱内执行完整 shell 命令（管道/重定向/环境变量），跑外部 CLI 工具。命令以 root 身份执行，sudo 不可用也无必要；预装清单外的命令也能写，但未安装的会报 command not found。"
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
		return registry.ToolResult{Error: "run_command: 解析参数失败: " + err.Error()}, nil
	}
	if a.Command == "" {
		return registry.ToolResult{Error: "run_command: command 必填"}, nil
	}
	if a.TimeoutSeconds <= 0 {
		a.TimeoutSeconds = defaultCommandTimeout
	}
	// 钳制上界 = runtime.step_tool_timeout_seconds（系统配置，注册表级超时同源）——
	// 防本地工具卡死；LLM 自传更长的 timeout 会被截到上界。
	if ceiling := runCommandTimeout(t.deps); a.TimeoutSeconds > int(ceiling.Seconds()) {
		a.TimeoutSeconds = int(ceiling.Seconds())
	}

	req := sandbox.ExecRequest{
		AssignmentID:   t.deps.AssignmentID,
		TaskID:         t.deps.TaskID,
		AgentRunID:     t.deps.AgentRunID,
		Command:        a.Command,
		TimeoutSeconds: a.TimeoutSeconds,
		Tag:            a.Tag,
	}
	res, err := t.deps.Sandbox.Exec(ctx, req)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("run_command: 沙箱执行失败: %v", err)}, nil
	}

	// stdout/stderr 合并后按 runtime.run_tail_bytes 保留尾部——命令输出的结果
	// （报文/报错）几乎总在末段，头部横幅与噪音优先弃。截断只影响给 LLM 的
	// Signal.Content；Output 保留全文供 tool_invocation 留档取证。
	combined := res.Stdout
	if res.Stderr != "" {
		if combined != "" {
			combined += "\n--- stderr ---\n"
		}
		combined += res.Stderr
	}
	visible := tailRunOutput(combined, t.deps.RunTailBytes)

	var sb strings.Builder
	sb.WriteString(visible)
	if res.TimedOut {
		fmt.Fprintf(&sb, "\n[超时: %ds]", a.TimeoutSeconds)
	}
	fmt.Fprintf(&sb, "\n[exit_code: %d]", res.ExitCode)

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

	output := sb.String()
	return registry.ToolResult{
		Output: output,
		Signal: &registry.Signal{
			Kind:     registry.SignalCmdOutput,
			ToolName: "run_command",
			Content:  output,
			Detail:   combined,
		},
	}, nil
}

// runCommandTimeout 返回 run_command 的注册表级超时 = runtime.step_tool_timeout_seconds
// （DB 旋钮，任务开始时快照；0=内置默认 120s）。它与 Execute 内的钳制上界同源——
// 此前注册表硬编码 120s 而配置写 1800s，LLM 自传长超时实际仍被 120s 掐死。
func runCommandTimeout(deps Deps) time.Duration {
	if deps.StepToolTimeoutSeconds > 0 {
		return time.Duration(deps.StepToolTimeoutSeconds) * time.Second
	}
	return constants.ToolTimeoutCommand
}

// tailRunOutput 保留 s 的尾部 n 字节（n<=0 用默认 8192）。按字节截断可能切断
// 多字节 UTF-8 字符——从截断点回退到字符起始边界再切。
func tailRunOutput(s string, n int) string {
	if n <= 0 {
		n = defaultRunTailBytes
	}
	b := []byte(s)
	if len(b) <= n {
		return s
	}
	cut := len(b) - n
	for cut < len(b) && cut > 0 && b[cut]&0xC0 == 0x80 {
		cut-- // 回退到 UTF-8 多字节序列的起始字节
	}
	trimmed := b[cut:]
	head := ""
	if cut > 0 {
		head = "\n...[前段已截断]\n"
	}
	return head + string(trimmed)
}
