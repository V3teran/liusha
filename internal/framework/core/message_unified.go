package core

import (
	"encoding/json"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// Message 是对话消息（业务层封装）。
// 内嵌 llm.Message（协议层），扩展 ID、时间戳、元数据。
type Message struct {
	// 消息唯一标识
	ID string `json:"id"`

	// LLM 协议层消息（Role/Content/Parts/ToolCalls 等）
	llm.Message `json:",inline"`

	// 消息元数据
	Metadata MessageMetadata `json:"metadata"`

	// 创建时间
	CreatedAt time.Time `json:"created_at"`
}

// MessageMetadata 消息元数据。
type MessageMetadata struct {
	// Token 数量（实际或估算）
	TokenCount int `json:"token_count,omitempty"`

	// 完成原因（finish_reason）
	FinishReason string `json:"finish_reason,omitempty"`

	// 模型标识
	Model string `json:"model,omitempty"`

	// 自定义标签
	Tags map[string]string `json:"tags,omitempty"`
}

// NewMessage 创建消息。
func NewMessage(id string, llmMsg llm.Message) *Message {
	return &Message{
		ID:        id,
		Message:   llmMsg,
		Metadata:  MessageMetadata{},
		CreatedAt: time.Now(),
	}
}

// NewTextMessage 创建纯文本消息。
func NewTextMessage(id string, role llm.Role, content string) *Message {
	return NewMessage(id, llm.Message{
		Role:    role,
		Content: content,
	})
}

// NewMultimodalMessage 创建多模态消息。
func NewMultimodalMessage(id string, role llm.Role, parts []llm.ContentPart) *Message {
	return NewMessage(id, llm.Message{
		Role:  role,
		Parts: parts,
	})
}

// TokenCount 返回 Token 数量（优先使用元数据中的实际值，否则估算）。
func (m *Message) TokenCount() int {
	if m.Metadata.TokenCount > 0 {
		return m.Metadata.TokenCount
	}

	// 简化估算：平均每 4 个字符约等于 1 个 token
	count := len(m.Content) / 4

	// 多模态内容估算
	for _, part := range m.Parts {
		if part.Type == "text" {
			count += len(part.Text) / 4
		} else if part.Type == "image" {
			// 图片固定估算 85 tokens（参考 Claude 视觉 token 消耗）
			count += 85
		}
	}

	return count
}

// Clone 克隆消息。
func (m *Message) Clone() *Message {
	data, _ := json.Marshal(m)
	var cloned Message
	_ = json.Unmarshal(data, &cloned)
	return &cloned
}

// ToLLMMessage 提取 LLM 协议层消息。
func (m *Message) ToLLMMessage() llm.Message {
	return m.Message
}

// ─────────────────────────────────────────────
//  类型别名（兼容旧代码）
// ─────────────────────────────────────────────

// MessageRole 是 llm.Role 的别名（保持向后兼容）。
type MessageRole = llm.Role

const (
	RoleSystem    = llm.RoleSystem
	RoleUser      = llm.RoleUser
	RoleAssistant = llm.RoleAssistant
	RoleTool      = llm.RoleTool
)

// ToolCall 是 llm.ToolCall 的别名。
type ToolCall = llm.ToolCall

// ContentPart 是 llm.ContentPart 的别名。
type ContentPart = llm.ContentPart

// ImageContent 是 llm.ImageContent 的别名。
type ImageContent = llm.ImageContent
