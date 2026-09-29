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

// SignalWeight 返回 SignalKind 对应的可信权重。
func SignalWeight(k SignalKind) float64 {
	switch k {
	case SignalCmdOutput:
		return 1.0
	case SignalHTTPTrace:
		return 0.9
	case SignalFileContent:
		return 0.8
	case SignalCredentialDump: // #nosec G101 // 枚举字面量，非凭证
		return 1.0
	case SignalNetworkScan:
		return 0.7
	case SignalScreenshot:
		return 0.3
	default:
		return 0.5
	}
}

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
//  Constraint（结构化约束）
// ─────────────────────────────────────────────

// ConstraintKind 是约束类型。
type ConstraintKind string

// ConstraintMaxSeverity 等枚举定义。
const (
	// ConstraintPassiveOnly 表示该工具仅限 passive 会话使用。
	ConstraintPassiveOnly ConstraintKind = "passive_only"
	// ConstraintNoDestructive / ConstraintPassiveOnly 是工具使用约束枚举。
	// ConstraintNoDestructive 禁止破坏性操作；ConstraintPassiveOnly 仅限被动会话；
	// ConstraintMaxSeverity 限制最高严重度。
	ConstraintNoDestructive ConstraintKind = "no_destructive"
	ConstraintMaxSeverity   ConstraintKind = "max_severity"
	ConstraintRateLimit     ConstraintKind = "rate_limit_rps"
)

// Constraint 是结构化执行约束，不靠 LLM 自我遵守。
type Constraint struct {
	Kind  ConstraintKind
	Value string // 约束参数，如 "medium"、"10"
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

// NewBaseTool 创建 BaseTool（带默认配置）。
func NewBaseTool() BaseTool {
	return BaseTool{
		timeout:         0,     // 使用全局默认
		concurrencySafe: false, // 默认不并发安全
	}
}
