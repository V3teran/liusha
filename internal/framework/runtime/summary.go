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

// completeForced 执行收尾的一次 LLM 调用，空响应时自适应重试。
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
	// 出站请求统一受预算治理：收尾调用前同样过修改链并写回（与 callLLM 的
	// 检查点语义同源）——历史以收敛基座进入收尾，重试注入的纠偏消息不会在
	// 未治理的历史上连续叠加（强制收尾是唯一绕过 callLLM 的出站点，不能成为
	// 预算治理的旁路）。
	if config.MessageModifierChain != nil {
		converged, err := config.MessageModifierChain.Apply(ctx, result.MessageHistory)
		if err != nil {
			return llm.Response{}, fmt.Errorf("message modifier chain failed: %w", err)
		}
		result.MessageHistory = converged
	}

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

// forceFinalAnswer 请模型再交一次终稿：why 说明为什么要（终稿没通过验收，或迭代已达上限；
// 为空时不说明），写在请求的开头。配了输出格式就按契约要——重申契约，模型交回的观察、判定、规划才取得
// 出来；没配就只要一份总结。得到的正文落进消息历史并成为终稿。
func (r *reactRuntime) forceFinalAnswer(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
	why string,
) error {
	var ask, retryWith string
	if config.wantsOutputFormat() {
		ask = config.OutputFormatPrompt
		if ask == "" {
			ask = "你刚才完成了任务，请按照以下格式输出："
		}
		if config.OutputFormatSchema != "" {
			ask += "\n\n" + config.OutputFormatSchema
		}
		ask += "\n\n要求：\n1. 必须是有效的JSON格式\n2. 基于你刚才执行的所有操作输出\n3. 不要再调用工具，立即输出JSON\n\n现在请输出："
		retryWith = "立即输出符合格式要求的 JSON"
	} else {
		ask = `请基于当前已收集的所有观察和执行结果，输出一个最终总结。

要求：
1. 使用 Final Answer: 开头标记最终答案
2. 总结你已经完成的工作和发现的关键信息
3. 如果任务未完全完成，说明原因和已完成的部分
4. 不要再调用任何工具

请立即输出最终总结。`
		retryWith = "输出最终总结"
	}
	// 收尾原因由 why 如实陈述——谎报「已达最大迭代次数」会污染下游推理：monitor 曾在
	// 总结里复述该假前提并据此给出交接建议。
	if why != "" {
		ask = why + "\n\n" + ask
	}
	result.AddUserMessage(ask)

	resp, err := r.completeForced(ctx, config, result, retryWith)
	if err != nil {
		return fmt.Errorf("force final answer failed: %w", err)
	}
	appendForcedResult(result, resp)
	return nil
}
