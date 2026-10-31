package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// maxForcedOutputTokens 是强制收尾请求的输出预算上限（翻倍重试的封顶值）。
// 封顶理由：超限预算会被 provider 直接拒绝，把「截断重试」变成「请求报错」。
const maxForcedOutputTokens = 16384

// completeForced 执行强制收尾的一次 LLM 调用，空响应时自适应重试。
//
// 收尾调用的输入是整轮消息历史（含全部思考块），扩展思考对新预算的消耗随
// 历史长度增长——固定预算下「思考吃满、正文被截断」是确定性失败，同预算
// 重试没有意义。这里按 2× 逐次放大输出预算（封顶 maxForcedOutputTokens），
// 并在重试间注入「直接输出正文」的纠偏指令。返回最后一次响应与错误；
// err 非 nil 时响应不可用。
func (r *reactRuntime) completeForced(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
	ask string,
) (llm.Response, error) {
	budget := config.MaxTokens
	if budget <= 0 {
		budget = maxForcedOutputTokens
	}
	var last llm.Response
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			budget *= 2
			if budget > maxForcedOutputTokens {
				budget = maxForcedOutputTokens
			}
			result.AddUserMessage("（上一条输出正文为空——生成被截断。" + ask + "，不要输出思考过程）")
		}
		request := llm.Request{
			Messages:  result.MessageHistory,
			MaxTokens: budget,
		}
		if config.Temperature > 0 {
			t := config.Temperature
			request.Temperature = &t
		}
		resp, err := config.LLMProvider.Complete(ctx, request)
		if err != nil {
			return llm.Response{}, fmt.Errorf("forced output LLM call failed: %w", err)
		}
		result.Usage = result.Usage.Add(resp.Usage)
		if strings.TrimSpace(resp.Content) != "" {
			return resp, nil
		}
		last = resp
	}
	return last, fmt.Errorf("forced output 连续 3 次空响应（输出预算 %d 仍被截断）", budget)
}

// appendForcedResult 把强制收尾得到的正文落进消息历史（思考块随行，供多轮
// tool_use 规范回传）。
func appendForcedResult(result *ReActResult, resp llm.Response) {
	result.AddAssistantMessage(resp.Content, nil, resp.Thinking)
	result.FinalThinking = thinkingText(resp.Thinking)
}

func (r *reactRuntime) forceFinalSummary(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) error {
	// 构造收尾总结的 prompt（收尾原因如实陈述——谎报「已达最大迭代次数」
	// 会污染下游推理：monitor 曾在总结里复述该假前提并据此给出交接建议）。
	summaryPrompt := `请基于当前已收集的所有观察和执行结果，输出一个最终总结。

要求：
1. 使用 Final Answer: 开头标记最终答案
2. 总结你已经完成的工作和发现的关键信息
3. 如果任务未完全完成，说明原因和已完成的部分
4. 不要再调用任何工具

请立即输出最终总结。`

	// 添加总结 prompt 到消息历史
	result.AddUserMessage(summaryPrompt)

	// 调用 LLM 获取最终总结（不允许工具调用；空响应自适应重试）
	resp, err := r.completeForced(ctx, config, result, "输出最终总结")
	if err != nil {
		return fmt.Errorf("force final summary failed: %w", err)
	}
	appendForcedResult(result, resp)
	return nil
}

// forceStructuredOutput 强制要求 LLM 输出结构化格式
// 当 LLM 自然停止但未输出符合期望格式的内容时调用
func (r *reactRuntime) forceStructuredOutput(
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

	// 调用 LLM 获取结构化输出（不允许工具调用；空响应自适应重试）
	resp, err := r.completeForced(ctx, config, result, "立即输出符合格式要求的 JSON")
	if err != nil {
		return fmt.Errorf("force structured output failed: %w", err)
	}
	appendForcedResult(result, resp)
	return nil
}
