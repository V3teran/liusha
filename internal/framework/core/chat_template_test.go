package core_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestChatTemplate_OpenAI 测试 OpenAI 格式
func TestChatTemplate_OpenAI(t *testing.T) {
	template := core.NewOpenAIChatTemplate()

	messages := []*core.Message{
		core.NewTextMessage("1", llm.RoleSystem, "You are a helpful assistant."),
		core.NewTextMessage("2", llm.RoleUser, "Hello!"),
		core.NewTextMessage("3", llm.RoleAssistant, "Hi there!"),
	}

	formatted, err := template.Format(messages)
	require.NoError(t, err)

	// 验证格式
	formattedSlice, ok := formatted.([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, formattedSlice, 3)

	// 验证第一条消息
	assert.Equal(t, "system", formattedSlice[0]["role"])
	assert.Equal(t, "You are a helpful assistant.", formattedSlice[0]["content"])

	// 验证第二条消息
	assert.Equal(t, "user", formattedSlice[1]["role"])
	assert.Equal(t, "Hello!", formattedSlice[1]["content"])

	// 验证第三条消息
	assert.Equal(t, "assistant", formattedSlice[2]["role"])
	assert.Equal(t, "Hi there!", formattedSlice[2]["content"])

	assert.Equal(t, "openai", template.ProviderName())
}

// TestChatTemplate_Claude 测试 Claude 格式
func TestChatTemplate_Claude(t *testing.T) {
	template := core.NewClaudeChatTemplate()

	messages := []*core.Message{
		core.NewTextMessage("1", llm.RoleSystem, "You are a helpful assistant."),
		core.NewTextMessage("2", llm.RoleUser, "Hello!"),
		core.NewTextMessage("3", llm.RoleAssistant, "Hi there!"),
	}

	formatted, err := template.Format(messages)
	require.NoError(t, err)

	// 验证格式
	formattedMap, ok := formatted.(map[string]interface{})
	require.True(t, ok)

	// 验证 system 被单独提取
	assert.Equal(t, "You are a helpful assistant.", formattedMap["system"])

	// 验证 messages
	msgs, ok := formattedMap["messages"].([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, msgs, 2) // system 不在 messages 中

	assert.Equal(t, "user", msgs[0]["role"])
	assert.Equal(t, "Hello!", msgs[0]["content"])

	assert.Equal(t, "assistant", msgs[1]["role"])
	assert.Equal(t, "Hi there!", msgs[1]["content"])

	assert.Equal(t, "claude", template.ProviderName())
}

// TestChatTemplate_GLM 测试 GLM 格式
func TestChatTemplate_GLM(t *testing.T) {
	template := core.NewGLMChatTemplate()

	messages := []*core.Message{
		core.NewTextMessage("1", llm.RoleSystem, "You are a helpful assistant."),
		core.NewTextMessage("2", llm.RoleUser, "Hello!"),
	}

	formatted, err := template.Format(messages)
	require.NoError(t, err)

	// 验证格式（类似 OpenAI）
	formattedSlice, ok := formatted.([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, formattedSlice, 2)

	assert.Equal(t, "glm", template.ProviderName())
}

// TestChatHistory 测试消息历史管理
func TestChatHistory(t *testing.T) {
	template := core.NewOpenAIChatTemplate()
	history := core.NewChatHistory(template)

	// 添加消息
	history.AddSystemMessage("1", "You are a helpful assistant.")
	history.AddUserMessage("2", "What is 2+2?")
	history.AddAssistantMessage("3", "The answer is 4.")

	// 验证消息数量
	assert.Equal(t, 3, history.Count())

	// 验证消息内容
	messages := history.GetMessages()
	assert.Len(t, messages, 3)
	assert.Equal(t, llm.RoleSystem, messages[0].Role)
	assert.Equal(t, llm.RoleUser, messages[1].Role)
	assert.Equal(t, llm.RoleAssistant, messages[2].Role)

	// 格式化
	formatted, err := history.Format()
	require.NoError(t, err)

	formattedSlice, ok := formatted.([]map[string]interface{})
	require.True(t, ok)
	assert.Len(t, formattedSlice, 3)

	// 清空
	history.Clear()
	assert.Equal(t, 0, history.Count())
}

// TestChatHistory_TokenCount 测试 token 计数
func TestChatHistory_TokenCount(t *testing.T) {
	template := core.NewOpenAIChatTemplate()
	history := core.NewChatHistory(template)

	// 添加消息
	history.AddUserMessage("1", "Hello") // 约 1-2 tokens
	history.AddAssistantMessage("2", "Hi there!") // 约 2-3 tokens

	// 验证总 token 数（简单估算）
	totalTokens := history.TotalTokens()
	assert.Greater(t, totalTokens, 0)
}

// TestMessageBuilder 测试消息构建器
func TestMessageBuilder(t *testing.T) {
	// 构建简单文本消息
	msg, err := core.NewMessageBuilder("msg-1").
		WithRole(llm.RoleUser).
		WithContent("Hello, world!").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "msg-1", msg.ID)
	assert.Equal(t, llm.RoleUser, msg.Role)
	assert.Equal(t, "Hello, world!", msg.Content)
}

// TestMessageBuilder_Multimodal 测试多模态消息构建
func TestMessageBuilder_Multimodal(t *testing.T) {
	msg, err := core.NewMessageBuilder("msg-1").
		WithRole(llm.RoleUser).
		WithTextPart("What's in this image?").
		WithImagePart("image/png", "base64data...").
		Build()

	require.NoError(t, err)
	assert.Equal(t, "msg-1", msg.ID)
	assert.Equal(t, llm.RoleUser, msg.Role)
	assert.Len(t, msg.ContentParts, 2)

	// 验证文本部分
	assert.Equal(t, "text", msg.ContentParts[0].Type)
	assert.Equal(t, "What's in this image?", msg.ContentParts[0].Text)

	// 验证图片部分
	assert.Equal(t, "image_url", msg.ContentParts[1].Type)
	assert.NotNil(t, msg.ContentParts[1].ImageURL)
	assert.Equal(t, "image/png", msg.ContentParts[1].ImageURL.MediaType)
	assert.Equal(t, "base64data...", msg.ContentParts[1].ImageURL.Base64Data)
}

// TestMessageBuilder_WithMetadata 测试元数据设置
func TestMessageBuilder_WithMetadata(t *testing.T) {
	msg, err := core.NewMessageBuilder("msg-1").
		WithRole(llm.RoleAssistant).
		WithContent("Response").
		WithTokenCount(10).
		WithModel("gpt-4").
		Build()

	require.NoError(t, err)
	assert.Equal(t, 10, msg.Metadata.TokenCount)
	assert.Equal(t, "gpt-4", msg.Metadata.Model)
}

// TestMessageBuilder_Validation 测试构建器验证
func TestMessageBuilder_Validation(t *testing.T) {
	// 缺少 ID
	_, err := core.NewMessageBuilder("").
		WithRole(llm.RoleUser).
		WithContent("Hello").
		Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "id is required")

	// 缺少 Role
	_, err = core.NewMessageBuilder("msg-1").
		WithContent("Hello").
		Build()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "role is required")
}

// TestChatTemplate_Factory 测试模板工厂
func TestChatTemplate_Factory(t *testing.T) {
	// 测试不同提供商
	openai := core.NewChatTemplate("openai")
	assert.Equal(t, "openai", openai.ProviderName())

	claude := core.NewChatTemplate("claude")
	assert.Equal(t, "claude", claude.ProviderName())

	glm := core.NewChatTemplate("glm")
	assert.Equal(t, "glm", glm.ProviderName())

	// 默认使用 OpenAI
	unknown := core.NewChatTemplate("unknown")
	assert.Equal(t, "openai", unknown.ProviderName())
}

// TestChatHistory_MultiProvider 测试不同提供商的历史
func TestChatHistory_MultiProvider(t *testing.T) {
	messages := []*core.Message{
		core.NewTextMessage("1", llm.RoleSystem, "You are helpful."),
		core.NewTextMessage("2", llm.RoleUser, "Hi"),
	}

	// OpenAI 格式
	openaiHistory := core.NewChatHistory(core.NewOpenAIChatTemplate())
	for _, msg := range messages {
		openaiHistory.AddMessage(msg)
	}
	openaiFormatted, _ := openaiHistory.Format()
	openaiSlice := openaiFormatted.([]map[string]interface{})
	assert.Len(t, openaiSlice, 2)

	// Claude 格式
	claudeHistory := core.NewChatHistory(core.NewClaudeChatTemplate())
	for _, msg := range messages {
		claudeHistory.AddMessage(msg)
	}
	claudeFormatted, _ := claudeHistory.Format()
	claudeMap := claudeFormatted.(map[string]interface{})
	assert.NotNil(t, claudeMap["system"])
	assert.Len(t, claudeMap["messages"].([]map[string]interface{}), 1)
}
