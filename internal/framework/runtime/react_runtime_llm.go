package runtime

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/llm"
)

func (r *DefaultReActRuntime) toolFailureLimit(config *ReActConfig) int {
	if config.MaxConsecutiveToolFailures > 0 {
		return config.MaxConsecutiveToolFailures
	}
	return 8
}

// callLLM 调用 LLM
func (r *DefaultReActRuntime) callLLM(
	ctx context.Context,
	config *ReActConfig,
	messageHistory []llm.Message,
) (*llmResponse, error) {
	// 应用消息预处理链（如果配置了）
	processedMessages := messageHistory
	if config.MessageModifierChain != nil {
		var err error
		processedMessages, err = config.MessageModifierChain.Apply(ctx, messageHistory)
		if err != nil {
			return nil, fmt.Errorf("message modifier chain failed: %w", err)
		}
	}

	// 消息已经是 llm.Message 格式，直接使用
	llmMessages := processedMessages

	// 构建 LLM 请求（温度按调用透传——Validate 已保证 0-1，>0 才有意义下发）
	request := llm.Request{
		Messages:  llmMessages,
		MaxTokens: config.MaxTokens,
	}
	if config.Temperature > 0 {
		t := config.Temperature
		request.Temperature = &t
	}

	// 添加工具定义（函数调用）
	if len(r.tools) > 0 {
		request.Tools = r.convertToolsToLLMFormat()
	}

	// 调用 LLM（非流式 Complete）。
	response, err := config.LLMProvider.Complete(ctx, request)
	if err != nil {
		return nil, err
	}

	// 解析响应
	return r.parseLLMResponse(response)
}

// llmResponse 是 LLM 响应的内部表示
type llmResponse struct {
	Content   string
	ToolCalls []llm.ToolCall
}

// convertToolsToLLMFormat 转换工具为 LLM 格式
func (r *DefaultReActRuntime) convertToolsToLLMFormat() []llm.ToolSchema {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]llm.ToolSchema, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, llm.ToolSchema{
			Name:        tool.Name(),
			Description: tool.Desc(),
			Parameters:  tool.Schema(),
		})
	}

	return tools
}

// parseLLMResponse 解析 LLM 响应
