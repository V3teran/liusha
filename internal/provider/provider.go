// Package provider 定义 LLM Provider 抽象接口及共享类型。
//
// 与 internal/llm 的区别：
//   - 支持流式输出（Stream）
//   - 支持精确 token 计数（CountTokens），非估算
//   - 不依赖外部 LLM 框架，可独立测试替换
//
// 依赖方向：provider ← executor ← dispatcher ← cognition
// provider 本身不引用项目内任何业务包。
package provider

import (
	"context"
	"encoding/json"
	"time"
)

// Provider 是 LLM 调用的统一抽象。实现者负责协议差异屏蔽。
//
// 并发安全：同一 Provider 实例可被多个 goroutine 并发调用。
type Provider interface {
	// Complete 发起单次非流式调用，等待完整响应。
	Complete(ctx context.Context, req Request) (Response, error)

	// Stream 发起流式调用，返回事件通道。
	// 调用方负责消费直到通道关闭；ctx 取消时通道在当前事件后关闭。
	// 通道发出 StreamEventError 后随即关闭。
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)

	// CountTokens 精确计算 req 的输入 token 数。
	// 不消耗生成配额；Anthropic 有专用端点，OpenAI 兼容层用本地 tiktoken。
	CountTokens(ctx context.Context, req Request) (int, error)

	// ModelID 返回底层模型标识，如 "claude-sonnet-4-6"。
	ModelID() string

	// ProviderID 返回 provider 标识，如 "anthropic" / "openai"。
	ProviderID() string
}

// ─────────────────────────────────────────────
//  请求 / 响应类型
// ─────────────────────────────────────────────

// Request 是一次 LLM 调用的完整输入。
type Request struct {
	Messages  []Message    // 会话历史，含 system/user/assistant/tool
	Tools     []ToolSchema // 可调用工具声明；空表示纯文本对话
	MaxTokens int          // 0 = provider 默认值
}

// Response 是非流式调用的完整输出。
type Response struct {
	Content      string     // 文本内容（ToolCalls 非空时可能为空）
	ToolCalls    []ToolCall // 工具调用请求列表
	Usage        Usage
	FinishReason string // "stop" | "tool_use" | "max_tokens" | "error"
}

// ─────────────────────────────────────────────
//  消息类型
// ─────────────────────────────────────────────

// Role 是消息角色。
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是一条会话消息。
//
// 纯文本走 Content；多模态走 Parts（两者互斥）。
// ToolCallID 与 RoleTool 搭配，标识这条消息对应哪个工具调用结果。
type Message struct {
	Role       Role          `json:"role"`
	Content    string        `json:"content,omitempty"`
	Parts      []ContentPart `json:"parts,omitempty"`
	Name       string        `json:"name,omitempty"`
	ToolCallID string        `json:"tool_call_id,omitempty"`
	ToolCalls  []ToolCall    `json:"tool_calls,omitempty"`
}

// ContentPart 是多模态消息的内容块。
// Type ∈ {"text", "image"}。
type ContentPart struct {
	Type      string        `json:"type"`
	Text      string        `json:"text,omitempty"`
	ImageData *ImageContent `json:"image,omitempty"`
}

// ImageContent 是 base64 内联图片（不引用外部 URL，避免沙箱网络依赖）。
type ImageContent struct {
	MediaType  string `json:"media_type"` // "image/png" | "image/jpeg" | "image/webp"
	Base64Data string `json:"data"`
}

// ToolCall 是模型请求执行的工具调用。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"` // JSON 对象
}

// ToolSchema 描述一个工具的名称、说明和参数 JSON Schema。
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema object
}

// Usage 是一次调用的 token 计量。
type Usage struct {
	InTokens     int
	OutTokens    int
	CachedTokens int
}

// Add 返回累加结果（值方法，不修改 receiver）。
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InTokens:     u.InTokens + o.InTokens,
		OutTokens:    u.OutTokens + o.OutTokens,
		CachedTokens: u.CachedTokens + o.CachedTokens,
	}
}

// ─────────────────────────────────────────────
//  流式事件
// ─────────────────────────────────────────────

// StreamEventKind 是流式事件的类型。
type StreamEventKind string

const (
	// StreamThinking 是 extended thinking 内容块（Anthropic 专有）。
	StreamThinking StreamEventKind = "thinking"
	// StreamText 是生成文本增量。
	StreamText StreamEventKind = "text"
	// StreamToolCall 是完整的工具调用请求（arguments 已收全）。
	StreamToolCall StreamEventKind = "tool_call"
	// StreamDone 标记流结束，携带最终 Usage。
	StreamDone StreamEventKind = "done"
	// StreamError 标记流出错，携带 Err 字段。
	StreamError StreamEventKind = "error"
)

// StreamEvent 是 Provider.Stream 通道上的一个事件。
type StreamEvent struct {
	Kind    StreamEventKind
	Content string    // StreamThinking / StreamText 有效
	Tool    *ToolCall // StreamToolCall 有效
	Usage   *Usage    // StreamDone 有效
	Err     error     // StreamError 有效
}

// ─────────────────────────────────────────────
//  重试 / 错误类型
// ─────────────────────────────────────────────

// HTTPError 表示上游返回的 HTTP 错误，用于重试分类。
type HTTPError struct {
	Code  int
	Inner error
}

func (e *HTTPError) Error() string {
	if e.Inner != nil {
		return e.Inner.Error()
	}
	return "http error " + itoa(e.Code)
}
func (e *HTTPError) Unwrap() error { return e.Inner }

// RetryConfig 控制重试行为。
// 5xx 可重试，4xx / context cancel 不重试。
// 架构规格：最多 3 次，指数退避 1s/2s/4s。
type RetryConfig struct {
	Max      int             // 默认 3
	Backoffs []time.Duration // 长度须 ≥ Max；默认 [1s, 2s, 4s]
}

// DefaultRetryConfig 返回架构规格退避表。
func DefaultRetryConfig() RetryConfig {
	return RetryConfig{
		Max:      3,
		Backoffs: []time.Duration{time.Second, 2 * time.Second, 4 * time.Second},
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	buf := [20]byte{}
	pos := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		pos--
		buf[pos] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
