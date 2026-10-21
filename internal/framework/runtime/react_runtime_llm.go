package runtime

import (
	"context"
	"errors"
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

	// 调用 LLM：装配了思考增量回调时走流式（逐段回调 + 聚合出同构 Response），
	// Provider 不支持流式则降级非流式 Complete。
	var response llm.Response
	var err error
	if config.OnThoughtDelta != nil {
		response, err = r.callLLMStreaming(ctx, config, request)
	} else {
		response, err = config.LLMProvider.Complete(ctx, request)
	}
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
	// Thinking 是该轮扩展思考块（含回传签名；无思考为 nil）。
	Thinking *llm.ThinkingBlock
	// Usage 是该次调用的 token 用量（供 ReActResult.Usage 逐迭代累计）。
	Usage llm.Usage
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

// callLLMStreaming 流式调用并聚合为完整响应：正文/思考增量分别即时回调
// OnThoughtDelta / OnThinkingDelta（双通道分流），聚合口径与非流式一致
// （framework CollectStream 单一事实源）。Provider 无流式能力时降级 Complete
// （该轮无增量回调，整段思考经 llmResponse.Thinking 随终稿到达）。
func (r *DefaultReActRuntime) callLLMStreaming(
	ctx context.Context,
	config *ReActConfig,
	request llm.Request,
) (llm.Response, error) {
	ch, err := config.LLMProvider.Stream(ctx, request)
	if err != nil {
		if errors.Is(err, llm.ErrStreamingUnsupported) {
			// 降级不记日志：本轮 OnThought 仍有整段回调，行为面无感。
			return config.LLMProvider.Complete(ctx, request)
		}
		return llm.Response{}, err
	}
	return llm.CollectStream(ctx, ch, config.OnThoughtDelta, config.OnThinkingDelta)
}
