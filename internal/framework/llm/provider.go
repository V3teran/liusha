// Package provider 定义 LLM Provider 抽象接口及共享类型。
//
// 与 internal/llm 的区别：
//   - 支持流式输出（Stream）
//   - 支持精确 token 计数（CountTokens），非估算
//   - 不依赖外部 LLM 框架，可独立测试替换
//
// 依赖方向：provider ← executor ← dispatcher ← cognition
// provider 本身不引用项目内任何业务包。
package llm

import (
	"context"
	"time"

	corellm "github.com/V3teran/liusha/internal/llm"
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
	Messages  []corellm.Message    // 会话历史，含 system/user/assistant/tool
	Tools     []corellm.ToolSchema // 可调用工具声明；空表示纯文本对话
	MaxTokens int                  // 0 = provider 默认值
}

// Response 是非流式调用的完整输出。
type Response struct {
	Content      string             // 文本内容（ToolCalls 非空时可能为空）
	ToolCalls    []corellm.ToolCall // 工具调用请求列表
	Usage        corellm.Usage
	FinishReason string // "stop" | "tool_use" | "max_tokens" | "error"
}

// ─────────────────────────────────────────────
//  类型别名（统一到 internal/llm）
// ─────────────────────────────────────────────

// Message 是会话消息（别名到 internal/llm）。
type Message = corellm.Message

// Role 是消息角色（别名到 internal/llm）。
type Role = corellm.Role

const (
	RoleSystem    = corellm.RoleSystem
	RoleUser      = corellm.RoleUser
	RoleAssistant = corellm.RoleAssistant
	RoleTool      = corellm.RoleTool
)

// ContentPart 是多模态消息的内容块（别名到 internal/llm）。
type ContentPart = corellm.ContentPart

// ImageContent 是 base64 内联图片（别名到 internal/llm）。
type ImageContent = corellm.ImageContent

// ToolCall 是模型请求执行的工具调用（别名到 internal/llm）。
type ToolCall = corellm.ToolCall

// ToolSchema 描述一个工具的名称、说明和参数 JSON Schema（别名到 internal/llm）。
type ToolSchema = corellm.ToolSchema

// Usage 是一次调用的 token 计量（别名到 internal/llm）。
type Usage = corellm.Usage

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
