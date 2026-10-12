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
	result.AddAssistantMessage(resp.Content, nil)

	return nil
}
