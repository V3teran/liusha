// Package llm 是框架层 LLM 抽象——Provider 契约 + 类型事实源 + 协议适配器。
//
// 分层（Ports & Adapters）：
//   - 本包定义 Provider 契约（Complete/Stream/CountTokens）与全部线类型
//     （Message/ToolCall/Usage...），并持有唯一的协议适配器实现
//     （openai_compat / anthropic，血统为生产验证过的 4 月版本）。
//   - 业务侧（cmd/api 等）按 role 归档（llmconfig.AgentComplexity）后走本包 Router；
//     复杂度归档表在 internal/llmconfig（DB 配置优先，代码兜底）。
//
// 依赖方向：本包不 import 任何业务包。
package llm

import (
	"context"
	"errors"
)

// Provider 是 LLM 调用的统一抽象。实现者负责协议差异屏蔽。
//
// 并发安全：同一 Provider 实例可被多个 goroutine 并发调用。
type Provider interface {
	// Complete 发起单次非流式调用，等待完整响应。
	Complete(ctx context.Context, req Request) (Response, error)

	// Stream 发起流式调用，返回事件通道：文本增量 / 聚合后的完整工具调用 /
	// 收尾（带 usage）。调用方消费直到通道关闭；出错时通道发出 StreamError
	// 事件后关闭。底层 Generator 无流式能力时返回 ErrStreamingUnsupported
	// （调用方可据此降级 Complete）。
	Stream(ctx context.Context, req Request) (<-chan StreamEvent, error)

	// CountTokens 计算 req 的输入 token 数（OpenAI 兼容层为本地估算）。
	// 不消耗生成配额。
	CountTokens(ctx context.Context, req Request) (int, error)

	// ModelID 返回底层模型标识。
	ModelID() string

	// ProviderID 返回 provider 标识，如 "anthropic" / "openai"。
	ProviderID() string
}

// StreamEventKind 是流式事件的类型。
type StreamEventKind string

const (
	// StreamText 是文本增量事件。
	StreamText StreamEventKind = "text"
	// StreamThinking 是思考增量事件（anthropic 扩展思考流）。
	StreamThinking StreamEventKind = "thinking"
	// StreamToolCall 是聚合完成后的完整工具调用事件。
	StreamToolCall StreamEventKind = "tool_call"
	// StreamDone 是正常收尾事件（携带 Usage）。
	StreamDone StreamEventKind = "done"
	// StreamError 是异常收尾事件（Err 非空）。
	StreamError StreamEventKind = "error"
)

// StreamEvent 是流式调用的一帧事件。
type StreamEvent struct {
	Kind    StreamEventKind
	Content string    // StreamText/StreamThinking 有效
	Tool    *ToolCall // StreamToolCall 有效
	Usage   *Usage    // StreamDone 有效
	Err     error     // StreamError 有效
	// Signature 是思考块的回传校验签名，StreamDone 有效（anthropic 流在收尾时
	// 才给出完整 signature；与 StreamThinking 增量聚合的 Text 组成 ThinkingBlock）。
	Signature string
}

// ErrStreamingUnsupported 表示底层 Generator 无流式能力（桥接层判定），
// 调用方可用 errors.Is 识别并降级为 Complete。
var ErrStreamingUnsupported = errors.New("llm: streaming unsupported by generator")

// Request 是一次 LLM 调用的完整输入。
type Request struct {
	Messages  []Message    // 会话历史，含 system/user/assistant/tool
	Tools     []ToolSchema // 可调用工具声明；空表示纯文本对话
	MaxTokens int          // 0 = provider 默认值
	// Temperature 采样温度（0-1）；nil = provider 默认。指针区分「未设置」与 0
	// （0 = 贪心解码是有效语义，不能当默认吞掉）。
	Temperature *float64
}

// Response 是一次调用的完整输出（非流式直出；流式由 CollectStream 聚合得到同构结果）。
//
// json tag 是审计契约的一部分：usage.go 埋点把 Response 直接 json.Marshal 进
// llm_invocation.result，前端抽屉与列表 SQL 派生列（tool_names/text_preview）都按
// 小写键读取——无 tag 会序列化成 Go 大写字段名，整条审计渲染链失效。
type Response struct {
	Content      string         `json:"content,omitempty"`       // 文本内容（ToolCalls 非空时可能为空）
	ToolCalls    []ToolCall     `json:"tool_calls,omitempty"`    // 工具调用请求列表
	Usage        Usage          `json:"usage"`                   // token 用量
	FinishReason string         `json:"finish_reason,omitempty"` // "stop" | "tool_use" | "max_tokens" | "error"
	Thinking     *ThinkingBlock `json:"thinking,omitempty"`      // 扩展思考块（Text=增量聚合，Signature=收尾透出）
}
