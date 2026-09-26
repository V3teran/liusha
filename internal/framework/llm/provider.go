// Package llm：框架层 LLM 抽象——Provider 契约 + 类型事实源 + 协议适配器。
//
// 分层（Ports & Adapters）：
//   - 本包定义 Provider 契约（Complete/Stream/CountTokens）与全部线类型
//     （Message/ToolCall/Usage...），并持有唯一的协议适配器实现
//     （openai_compat / anthropic，血统为生产验证过的 4 月版本）。
//   - internal/llm 是业务策略层：role 路由、fallback 编排、审计 instrumentation，
//     类型全部别名到本包。
//
// 依赖方向：internal/llm → 本包；本包不 import 任何业务包。
package llm

import (
	"context"
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

	// CountTokens 计算 req 的输入 token 数（OpenAI 兼容层为本地估算）。
	// 不消耗生成配额。
	CountTokens(ctx context.Context, req Request) (int, error)

	// ModelID 返回底层模型标识。
	ModelID() string

	// ProviderID 返回 provider 标识，如 "anthropic" / "openai"。
	ProviderID() string
}

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

// StreamEventKind 是流式事件的类型。
type StreamEventKind string

const (
	StreamText     StreamEventKind = "text"
	StreamThinking StreamEventKind = "thinking" // anthropic 扩展思考增量
	StreamToolCall StreamEventKind = "tool_call"
	StreamDone     StreamEventKind = "done"
	StreamError    StreamEventKind = "error"
)

// StreamEvent 是流式调用的事件。
type StreamEvent struct {
	Kind    StreamEventKind
	Content string     // StreamText 有效
	Tool    *ToolCall  // StreamToolCall 有效
	Usage   *Usage     // StreamDone 有效
	Err     error      // StreamError 有效
}
