// types.go：LLM 抽象的类型事实源（框架层自包含，不 import 任何业务包）。
// Package llm 是框架层 LLM 抽象：Provider 契约 + 类型事实源 + 协议适配器。
// 业务策略（role 路由 / 审计 / fallback 编排）在 llmstore 与各业务包之上装配。

package llm

import (
	"context"
	"encoding/json"
	"fmt"
)

// MessageRole 是 LLM 消息角色。
type MessageRole string

// RoleUser 等枚举定义。
const (
	// RoleSystem 等枚举消息角色（OpenAI/Anthropic 协议通用四态）。
	RoleSystem    MessageRole = "system"
	RoleUser      MessageRole = "user"
	RoleAssistant MessageRole = "assistant"
	RoleTool      MessageRole = "tool"
)

// Message 是一次会话中的一条消息。
//
// Content 与 ContentParts 互斥：纯文本走 Content（默认路径）；含图/多模态走 ContentParts。
// ContentParts 非空时 provider 必须支持 vision；否则返 ErrVisionUnsupported。
// Thinking 是 assistant 消息携带的扩展思考块（anthropic 多轮 tool_use 规范回传用；
// 其余角色恒为 nil）。
type Message struct {
	Role         MessageRole    `json:"role"`
	Content      string         `json:"content,omitempty"`
	ContentParts []ContentPart  `json:"content_parts,omitempty"`
	Name         string         `json:"name,omitempty"`
	ToolCallID   string         `json:"tool_call_id,omitempty"`
	ToolCalls    []ToolCall     `json:"tool_calls,omitempty"`
	Thinking     *ThinkingBlock `json:"thinking,omitempty"`
}

// ContentPart 是 multimodal message 的内容块。Type ∈ {"text", "image_url"}。
//   - text：Text 字段有效
//   - image_url：ImageURL 字段有效
//
// 注意：image part 的 wire 值是 "image_url"（OpenAI 兼容协议命名），
// 所有协议适配器必须以此字面量判定，禁止另造 "image" 等变体。
type ContentPart struct {
	Type     string        `json:"type"`
	Text     string        `json:"text,omitempty"`
	ImageURL *ImageContent `json:"image_url,omitempty"`
}

// ImageContent 是图片内容（base64 内联，无外部 URL 避免沙箱网络依赖）。
// MediaType 形如 "image/png" / "image/jpeg"。
type ImageContent struct {
	MediaType  string `json:"media_type"`
	Base64Data string `json:"base64_data"`
}

// ToolCall 是模型请求调用工具的结构。Arguments 为 JSON 串（OpenAI 风格）。
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// ThinkingBlock 是模型的扩展思考块（协议原生形态：anthropic thinking block /
// openai 系 reasoning_content 的归一表示）。
//
// Text 是思考原文（流式增量聚合而成，供时间线展示与落库）；Signature 是协议
// 回传校验签名——anthropic 规范要求：开启扩展思考的多轮 tool_use 中，assistant
// 消息须原样回传 thinking block（含签名），故随 Message 持久携带。
type ThinkingBlock struct {
	Text      string `json:"text"`
	Signature string `json:"signature,omitempty"`
}

// ToolSchema 描述一个工具的元信息（名字 + 描述 + JSON Schema 参数）。
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Usage 是一次调用的 token 用量。
// json tag 同样是审计契约（Response 注释）：埋点直出进 llm_invocation.result。
type Usage struct {
	InTokens     int `json:"in_tokens"`
	OutTokens    int `json:"out_tokens"`
	CachedTokens int `json:"cached_tokens"`
}

// Add 返回两个 Usage 累加后的新值（值方法，不修改 receiver）。
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InTokens:     u.InTokens + o.InTokens,
		OutTokens:    u.OutTokens + o.OutTokens,
		CachedTokens: u.CachedTokens + o.CachedTokens,
	}
}

// Result 是 Generate 的返回结构。json tag 同 Response 注释（审计直出契约）。
type Result struct {
	Content      string     `json:"content,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	Usage        Usage      `json:"usage"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Provider     string     `json:"provider,omitempty"`
	Model        string     `json:"model,omitempty"`
	// Thinking 是本次生成的扩展思考块（模型内部 CoT；未开启/未产出为 nil）。
	Thinking *ThinkingBlock `json:"thinking,omitempty"`
}

// Generator 是 LLM 生成器统一接口（非流式）。
// 实现必须无状态：tools 每次调用动态传入，禁止在构造期绑定（避免跨 task 工具集错乱）。
type Generator interface {
	// Generate 发起一次会话生成。
	Generate(ctx context.Context, messages []Message, tools []ToolSchema) (Result, error)
	Provider() string
	Model() string
}

// GenerateOptions 是单次生成的可选参数（零值 = provider 默认）。
type GenerateOptions struct {
	Temperature *float64 // 采样温度；nil = 默认
}

// OptionsGenerator 是按调用传可选参数的能力接口。
// 适配器实现它后，Provider 桥接层会把 Request.Temperature 透传到协议请求。
type OptionsGenerator interface {
	GenerateWithOptions(ctx context.Context, messages []Message, tools []ToolSchema, opts GenerateOptions) (Result, error)
}

// HTTPError 是上游 HTTP 错误的统一表示。
// provider 适配层可在拿到非 2xx 时构造此错误（推荐 Inner 包裹原 error 便于排查）。
type HTTPError struct {
	Code  int
	Inner error
}

// Error 满足 error 接口。
func (e *HTTPError) Error() string {
	if e.Inner != nil {
		return fmt.Sprintf("http %d: %v", e.Code, e.Inner)
	}
	return fmt.Sprintf("http %d", e.Code)
}

// Unwrap 让 errors.Is/As 能穿透。
func (e *HTTPError) Unwrap() error { return e.Inner }
