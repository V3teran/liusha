package core

import (
	"fmt"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// ChatTemplate 是消息格式转换接口
// 不同 LLM 提供商有不同的消息格式要求
type ChatTemplate interface {
	// Format 将标准消息转换为提供商特定格式
	Format(messages []*Message) (interface{}, error)

	// FormatLLM 将 llm.Message 列表转换为提供商格式
	FormatLLM(messages []llm.Message) (interface{}, error)

	// ProviderName 返回提供商名称
	ProviderName() string
}

// OpenAIChatTemplate 是 OpenAI 格式的模板
type OpenAIChatTemplate struct{}

func NewOpenAIChatTemplate() ChatTemplate {
	return &OpenAIChatTemplate{}
}

func (t *OpenAIChatTemplate) Format(messages []*Message) (interface{}, error) {
	llmMessages := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		llmMessages = append(llmMessages, msg.ToLLMMessage())
	}
	return t.FormatLLM(llmMessages)
}

func (t *OpenAIChatTemplate) FormatLLM(messages []llm.Message) (interface{}, error) {
	// OpenAI 格式：[{role, content}]
	formatted := make([]map[string]interface{}, 0, len(messages))

	for _, msg := range messages {
		item := map[string]interface{}{
			"role":    string(msg.Role),
			"content": msg.Content,
		}

		// 工具调用
		if len(msg.ToolCalls) > 0 {
			toolCalls := make([]map[string]interface{}, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				toolCalls = append(toolCalls, map[string]interface{}{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      tc.Name,
						"arguments": tc.Arguments,
					},
				})
			}
			item["tool_calls"] = toolCalls
		}

		// 工具调用 ID（用于 tool role）
		if msg.ToolCallID != "" {
			item["tool_call_id"] = msg.ToolCallID
		}

		formatted = append(formatted, item)
	}

	return formatted, nil
}

func (t *OpenAIChatTemplate) ProviderName() string {
	return "openai"
}

// ClaudeChatTemplate 是 Claude 格式的模板
type ClaudeChatTemplate struct{}

func NewClaudeChatTemplate() ChatTemplate {
	return &ClaudeChatTemplate{}
}

func (t *ClaudeChatTemplate) Format(messages []*Message) (interface{}, error) {
	llmMessages := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		llmMessages = append(llmMessages, msg.ToLLMMessage())
	}
	return t.FormatLLM(llmMessages)
}

func (t *ClaudeChatTemplate) FormatLLM(messages []llm.Message) (interface{}, error) {
	// Claude 格式：system 单独，其他消息交替 user/assistant
	var systemPrompt string
	formatted := make([]map[string]interface{}, 0, len(messages))

	for _, msg := range messages {
		if msg.Role == llm.RoleSystem {
			systemPrompt = msg.Content
			continue
		}

		item := map[string]interface{}{
			"role":    string(msg.Role),
			"content": msg.Content,
		}

		// Claude 支持工具使用
		if len(msg.ToolCalls) > 0 {
			toolUse := make([]map[string]interface{}, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				toolUse = append(toolUse, map[string]interface{}{
					"type": "tool_use",
					"id":   tc.ID,
					"name": tc.Name,
					"input": tc.Arguments,
				})
			}
			item["content"] = toolUse
		}

		formatted = append(formatted, item)
	}

	result := map[string]interface{}{
		"messages": formatted,
	}

	if systemPrompt != "" {
		result["system"] = systemPrompt
	}

	return result, nil
}

func (t *ClaudeChatTemplate) ProviderName() string {
	return "claude"
}

// GLMChatTemplate 是智谱 GLM 格式的模板
type GLMChatTemplate struct{}

func NewGLMChatTemplate() ChatTemplate {
	return &GLMChatTemplate{}
}

func (t *GLMChatTemplate) Format(messages []*Message) (interface{}, error) {
	llmMessages := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		llmMessages = append(llmMessages, msg.ToLLMMessage())
	}
	return t.FormatLLM(llmMessages)
}

func (t *GLMChatTemplate) FormatLLM(messages []llm.Message) (interface{}, error) {
	// GLM 格式类似 OpenAI，但有一些特殊处理
	formatted := make([]map[string]interface{}, 0, len(messages))

	for _, msg := range messages {
		item := map[string]interface{}{
			"role":    string(msg.Role),
			"content": msg.Content,
		}

		// GLM 工具调用格式
		if len(msg.ToolCalls) > 0 {
			toolCalls := make([]map[string]interface{}, 0, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				toolCalls = append(toolCalls, map[string]interface{}{
					"id":        tc.ID,
					"type":      "function",
					"function": map[string]interface{}{
						"name":      tc.Name,
						"arguments": tc.Arguments,
					},
				})
			}
			item["tool_calls"] = toolCalls
		}

		formatted = append(formatted, item)
	}

	return formatted, nil
}

func (t *GLMChatTemplate) ProviderName() string {
	return "glm"
}

// NewChatTemplate 根据提供商名称创建模板
func NewChatTemplate(provider string) ChatTemplate {
	switch provider {
	case "openai":
		return NewOpenAIChatTemplate()
	case "claude":
		return NewClaudeChatTemplate()
	case "glm":
		return NewGLMChatTemplate()
	default:
		return NewOpenAIChatTemplate() // 默认使用 OpenAI 格式
	}
}

// ChatHistory 是消息历史管理器
type ChatHistory struct {
	messages []*Message
	template ChatTemplate
}

// NewChatHistory 创建新的消息历史
func NewChatHistory(template ChatTemplate) *ChatHistory {
	return &ChatHistory{
		messages: make([]*Message, 0),
		template: template,
	}
}

// AddMessage 添加消息
func (h *ChatHistory) AddMessage(msg *Message) {
	h.messages = append(h.messages, msg)
}

// AddTextMessage 添加文本消息
func (h *ChatHistory) AddTextMessage(id string, role llm.Role, content string) {
	msg := NewTextMessage(id, role, content)
	h.AddMessage(msg)
}

// AddSystemMessage 添加系统消息
func (h *ChatHistory) AddSystemMessage(id, content string) {
	h.AddTextMessage(id, llm.RoleSystem, content)
}

// AddUserMessage 添加用户消息
func (h *ChatHistory) AddUserMessage(id, content string) {
	h.AddTextMessage(id, llm.RoleUser, content)
}

// AddAssistantMessage 添加助手消息
func (h *ChatHistory) AddAssistantMessage(id, content string) {
	h.AddTextMessage(id, llm.RoleAssistant, content)
}

// GetMessages 获取所有消息
func (h *ChatHistory) GetMessages() []*Message {
	return h.messages
}

// Format 格式化消息为提供商格式
func (h *ChatHistory) Format() (interface{}, error) {
	return h.template.Format(h.messages)
}

// Clear 清空历史
func (h *ChatHistory) Clear() {
	h.messages = make([]*Message, 0)
}

// Count 返回消息数量
func (h *ChatHistory) Count() int {
	return len(h.messages)
}

// TotalTokens 返回总 token 数
func (h *ChatHistory) TotalTokens() int {
	total := 0
	for _, msg := range h.messages {
		total += msg.TokenCount()
	}
	return total
}

// MessageBuilder 是消息构建器（流式 API）
type MessageBuilder struct {
	id       string
	role     llm.Role
	content  string
	parts    []llm.ContentPart
	metadata MessageMetadata
}

// NewMessageBuilder 创建消息构建器
func NewMessageBuilder(id string) *MessageBuilder {
	return &MessageBuilder{
		id:       id,
		metadata: MessageMetadata{},
	}
}

// WithRole 设置角色
func (b *MessageBuilder) WithRole(role llm.Role) *MessageBuilder {
	b.role = role
	return b
}

// WithContent 设置内容
func (b *MessageBuilder) WithContent(content string) *MessageBuilder {
	b.content = content
	return b
}

// WithPart 添加内容部分
func (b *MessageBuilder) WithPart(part llm.ContentPart) *MessageBuilder {
	b.parts = append(b.parts, part)
	return b
}

// WithTextPart 添加文本部分
func (b *MessageBuilder) WithTextPart(text string) *MessageBuilder {
	return b.WithPart(llm.ContentPart{
		Type: "text",
		Text: text,
	})
}

// WithImagePart 添加图片部分（Base64）
func (b *MessageBuilder) WithImagePart(mediaType, base64Data string) *MessageBuilder {
	return b.WithPart(llm.ContentPart{
		Type: "image_url",
		ImageURL: &llm.ImageContent{
			MediaType:  mediaType,
			Base64Data: base64Data,
		},
	})
}

// WithTokenCount 设置 token 数
func (b *MessageBuilder) WithTokenCount(count int) *MessageBuilder {
	b.metadata.TokenCount = count
	return b
}

// WithModel 设置模型
func (b *MessageBuilder) WithModel(model string) *MessageBuilder {
	b.metadata.Model = model
	return b
}

// Build 构建消息
func (b *MessageBuilder) Build() (*Message, error) {
	if b.id == "" {
		return nil, fmt.Errorf("message id is required")
	}

	if b.role == "" {
		return nil, fmt.Errorf("message role is required")
	}

	msg := &Message{
		ID: b.id,
		Message: llm.Message{
			Role:         b.role,
			Content:      b.content,
			ContentParts: b.parts,
		},
		Metadata: b.metadata,
	}

	return msg, nil
}
