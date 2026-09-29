// types.go：LLM 抽象的类型事实源（框架层自包含，不 import 任何业务包）。
//
// internal/llm 里的同名类型全部是本文件的别名——业务层（role 路由 / 审计 /
// fallback 编排）在框架能力之上做策略装配。
// Package llm 是框架层 LLM 抽象：Provider 契约 + 类型事实源 + 协议适配器。
//
// internal/llm 是业务策略层（role 路由 / fallback 编排 / 审计），类型别名到本包。

package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// ErrVisionUnsupported 当 caller 传入含 image_url 的 ContentParts，但 provider 不支持视觉时返。
// 调用方应路由到 vision_provider 或 fail-fast。
var ErrVisionUnsupported = errors.New("llm: provider 不支持 vision，含图 message 需路由到 vision_provider")

// Role 是消息角色。
type Role string

// RoleUser 等枚举定义。
const (
	// RoleSystem 等枚举消息角色（OpenAI/Anthropic 协议通用四态）。
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// Message 是一次会话中的一条消息。
//
// Content 与 ContentParts 互斥：纯文本走 Content（默认路径）；含图/多模态走 ContentParts。
// ContentParts 非空时 provider 必须支持 vision；否则返 ErrVisionUnsupported。
type Message struct {
	Role         Role          `json:"role"`
	Content      string        `json:"content,omitempty"`
	ContentParts []ContentPart `json:"content_parts,omitempty"`
	Name         string        `json:"name,omitempty"`
	ToolCallID   string        `json:"tool_call_id,omitempty"`
	ToolCalls    []ToolCall    `json:"tool_calls,omitempty"`
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

// ToolSchema 描述一个工具的元信息（名字 + 描述 + JSON Schema 参数）。
type ToolSchema struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

// Usage 是一次调用的 token 用量。
type Usage struct {
	InTokens     int
	OutTokens    int
	CachedTokens int
}

// Add 返回两个 Usage 累加后的新值（值方法，不修改 receiver）。
func (u Usage) Add(o Usage) Usage {
	return Usage{
		InTokens:     u.InTokens + o.InTokens,
		OutTokens:    u.OutTokens + o.OutTokens,
		CachedTokens: u.CachedTokens + o.CachedTokens,
	}
}

// Result 是 Generate 的返回结构。
type Result struct {
	Content      string
	ToolCalls    []ToolCall
	Usage        Usage
	FinishReason string
	Provider     string
	Model        string
}

// Generator 是 LLM 生成器统一接口（非流式）。
// 实现必须无状态：tools 每次调用动态传入，禁止在构造期绑定（避免跨 task 工具集错乱）。
type Generator interface {
	// Generate 发起一次会话生成。
	Generate(ctx context.Context, messages []Message, tools []ToolSchema) (Result, error)
	Provider() string
	Model() string
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
