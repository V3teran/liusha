// Package registry 定义工具接口、执行结果类型、Signal 证据类型，
// 并实现带 Interceptor 链的 Registry。
//
// 依赖方向：registry ← executor ← dispatcher
package registry

import (
	"context"
	"encoding/json"
	"time"
)

// ─────────────────────────────────────────────
//  Signal（证据单元）
// ─────────────────────────────────────────────

// SignalKind 是证据的来源类型，决定可信权重。
type SignalKind string

// SignalFileContent 等枚举定义。
const (
	// SignalCmdOutput 等枚举证据来源类别，权重供 evidence 判定打分。
	SignalCmdOutput SignalKind = "cmd_output" // 权重 1.0
	// SignalHTTPTrace 等：证据来源类别枚举（注释为 evidence 打分权重）。
	// SignalCmdOutput 命令输出；SignalHTTPTrace HTTP 轨迹；SignalFileContent 文件内容； // #nosec G101 // 枚举字面量，非凭证
	// SignalCredentialDump 凭证导出；SignalNetworkScan 网络扫描；SignalScreenshot 截图。 // #nosec G101 // 枚举字面量，非凭证
	// 行尾数字为 evidence 打分权重。
	SignalHTTPTrace   SignalKind = "http_trace"   // 权重 0.9
	SignalFileContent SignalKind = "file_content" // 权重 0.8
	// #nosec G101 — credential_dump 是信号类别枚举值，非凭证。
	SignalCredentialDump SignalKind = "credential_dump" // 权重 1.0
	SignalNetworkScan    SignalKind = "network_scan"    // 权重 0.7
	SignalScreenshot     SignalKind = "screenshot"      // 权重 0.3
)

// Signal 是工具执行产生的一条证据。
type Signal struct {
	Kind       SignalKind
	ToolName   string // 产生此 Signal 的工具名
	Content    string // ≤4096 字符摘要，注入 LLM 上下文
	Detail     string // 完整内容，按需拉取
	Truncated  bool
	CapturedAt time.Time
}

// ─────────────────────────────────────────────
//  ToolResult / Tool
// ─────────────────────────────────────────────

// ToolResult 是工具执行的返回值。
// Error 非空时 Output 通常为空；Signal 携带可供 Verifier 使用的证据。
type ToolResult struct {
	Output string
	Signal *Signal
	Error  string // 工具错误的人类可读文本（非 Go error，不中断 ReAct 循环）
}

// Tool 是工具的统一接口。实现者提供名称、描述、Schema 和执行逻辑。
type Tool interface {
	// Name 是工具的唯一标识符，用于 LLM tool_call.name 字段。
	Name() string
	// ShortDesc 是一行简短说明（用于 Registry 日志和 SSE 显示）。
	ShortDesc() string
	// Desc 是完整说明，注入 ToolSchema.Description。
	Desc() string
	// Schema 返回工具的 JSON Schema（parameters 部分）。
	Schema() json.RawMessage
	// Execute 执行工具。args 是 LLM 传入的 JSON 参数对象。
	// 工具内部错误应以 ToolResult.Error 字符串返回，不返回 Go error，
	// 除非是 context.Canceled / context.DeadlineExceeded（这两种中断 ReAct 循环）。
	Execute(ctx context.Context, args json.RawMessage) (ToolResult, error)
	// Timeout 返回工具的执行超时时间（0 表示使用全局默认值）。
	Timeout() time.Duration
	// ConcurrencySafe 声明工具是否可与其他工具并发执行。
	ConcurrencySafe() bool
}

// ─────────────────────────────────────────────
//  BaseTool（提供默认实现）
// ─────────────────────────────────────────────

// BaseTool 为 Tool 接口提供默认实现，工具可嵌入此类型减少样板代码。
type BaseTool struct {
	timeout         time.Duration
	concurrencySafe bool
}

// Timeout 返回工具超时时间（0 = 使用全局默认 120 秒）。
func (b BaseTool) Timeout() time.Duration {
	return b.timeout
}

// ConcurrencySafe 返回工具是否并发安全（默认 false）。
func (b BaseTool) ConcurrencySafe() bool {
	return b.concurrencySafe
}

// WithTimeout 设置超时时间（构造器辅助方法）。
func (b *BaseTool) WithTimeout(d time.Duration) *BaseTool {
	b.timeout = d
	return b
}

// WithConcurrencySafe 设置并发安全标识（构造器辅助方法）。
func (b *BaseTool) WithConcurrencySafe(safe bool) *BaseTool {
	b.concurrencySafe = safe
	return b
}

// SetTimeout 设置超时时间（setter 方法）。
func (b *BaseTool) SetTimeout(d time.Duration) {
	b.timeout = d
}

// SetConcurrencySafe 设置并发安全标识（setter 方法）。
func (b *BaseTool) SetConcurrencySafe(safe bool) {
	b.concurrencySafe = safe
}

// Allows 报告 name 是否在白名单内。白名单语义全项目统一：
// nil = 全量放行；非 nil（含空切片）= 严格白名单，不在名单即拒绝。
// function_tools/cli_tools/skills 四处 agent 装配共用本判定，避免各写一份闭包。
func Allows(allowlist []string, name string) bool {
	if allowlist == nil {
		return true
	}
	for _, n := range allowlist {
		if n == name {
			return true
		}
	}
	return false
}
