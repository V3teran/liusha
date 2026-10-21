package runtime

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/llm"
)

func (r *DefaultReActRuntime) forceFinalSummary(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) error {
	// 构造强制总结的 prompt
	summaryPrompt := `你已经达到最大迭代次数限制。请基于当前已收集的所有观察和执行结果，输出一个最终总结。

要求：
1. 使用 Final Answer: 开头标记最终答案
2. 总结你已经完成的工作和发现的关键信息
3. 如果任务未完全完成，说明原因和已完成的部分
4. 不要再调用任何工具

请立即输出最终总结。`

	// 添加总结 prompt 到消息历史
	result.AddUserMessage(summaryPrompt)

	// 调用 LLM 获取最终总结（不允许工具调用）
	request := llm.Request{
		Messages:  result.MessageHistory,
		MaxTokens: config.MaxTokens,
	}
	if config.Temperature > 0 {
		t := config.Temperature
		request.Temperature = &t
	}
	// 注意：不添加 tools，禁止工具调用

	resp, err := config.LLMProvider.Complete(ctx, request)
	if err != nil {
		return fmt.Errorf("force final summary LLM call failed: %w", err)
	}

	// 将总结添加到消息历史
	result.AddAssistantMessage(resp.Content, nil, resp.Thinking)
	result.FinalThinking = thinkingText(resp.Thinking)

	return nil
}

// forceStructuredOutput 强制要求 LLM 输出结构化格式
// 当 LLM 自然停止但未输出符合期望格式的内容时调用
func (r *DefaultReActRuntime) forceStructuredOutput(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) error {
	// 检查是否配置了输出格式
	if config.OutputFormatPrompt == "" && config.OutputFormatSchema == "" {
		// 没有配置特定格式，使用通用总结
		return r.forceFinalSummary(ctx, config, result)
	}

	// 构造结构化输出 prompt
	var promptBuilder string
	if config.OutputFormatPrompt != "" {
		promptBuilder = config.OutputFormatPrompt
	} else {
		promptBuilder = "你刚才完成了任务，但是输出格式不符合系统要求。\n\n请按照以下格式输出："
	}

	if config.OutputFormatSchema != "" {
		promptBuilder += "\n\n" + config.OutputFormatSchema
	}

	promptBuilder += "\n\n要求：\n1. 必须是有效的JSON格式\n2. 基于你刚才执行的所有操作输出\n3. 不要再调用工具，立即输出JSON\n\n现在请输出："

	result.AddUserMessage(promptBuilder)

	// 调用 LLM 获取结构化输出（不允许工具调用）
	request := llm.Request{
		Messages:  result.MessageHistory,
		MaxTokens: config.MaxTokens,
	}
	if config.Temperature > 0 {
		t := config.Temperature
		request.Temperature = &t
	}

	resp, err := config.LLMProvider.Complete(ctx, request)
	if err != nil {
		return fmt.Errorf("force structured output LLM call failed: %w", err)
	}

	// 将结构化输出添加到消息历史
	result.AddAssistantMessage(resp.Content, nil, resp.Thinking)
	result.FinalThinking = thinkingText(resp.Thinking)

	return nil
}
