package runtime

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// initializeResult 初始化 ReActResult
func initializeResult() *ReActResult {
	return &ReActResult{
		MessageHistory: make([]llm.Message, 0),
		Trace:          make([]*IterationTrace, 0),
		Status:         ReActStatusSuccess,
	}
}

// initializeNewExecution 初始化新执行的消息历史
func (r *reactRuntime) initializeNewExecution(config *ReActConfig, result *ReActResult) {
	// 加载初始历史（可选，用于多轮对话）
	if len(config.InitialHistory) > 0 {
		result.MessageHistory = append(result.MessageHistory, config.InitialHistory...)
	}

	// 添加系统提示
	if config.SystemPrompt != "" {
		result.AddSystemMessage(config.SystemPrompt)
	}

	// 添加用户目标
	result.AddUserMessage(config.Objective)
}

// whyIterationLimit 是迭代达到上限时请模型收尾的说法。
const whyIterationLimit = "迭代已达上限，不能再调用工具。"

// whyNoFinalAnswer 是循环在模型交出终稿之前就被终止时请模型收尾的说法。
const whyNoFinalAnswer = "循环在交出终稿之前已被终止。请基于已经做完的事收尾。"

// handleMaxIterationsReached 处理达到最大迭代次数的情况：模型没有自己停下，请它收尾。
func (r *reactRuntime) handleMaxIterationsReached(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) {
	if err := r.forceFinalAnswer(ctx, config, result, whyIterationLimit); err != nil {
		// 收尾失败不算致命错误，继续处理
		result.InternalError = fmt.Errorf("force final answer failed: %w", err)
	}
}

// handleNormalCompletion 处理循环停下的情况。只有模型自己停下（不再调用工具，最后一条
// 消息是它的回复）才有终稿：验收通过就原样交付，不通过就带着原因请模型再交一次（终稿是
// 被输出上限截断的，原因是截断，见 finalDraftProblem）。循环在模型交出终稿之前被终止的
// （中间件止损、外部中止——此时最后一条是工具结果或中止说明），没有终稿可交付，请模型
// 基于已经做完的事收尾：执行者的观察、评估者的判定、规划者的动作都写在这份终稿里，少了
// 它整段执行白做。
func (r *reactRuntime) handleNormalCompletion(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) {
	why := whyNoFinalAnswer
	if last := result.MessageHistory[len(result.MessageHistory)-1]; last.Role == llm.RoleAssistant {
		why = r.finalDraftProblem(config, last.Content)
	}
	if why == "" {
		return
	}
	if err := r.forceFinalAnswer(ctx, config, result, why); err != nil {
		result.InternalError = fmt.Errorf("force final answer failed: %w", err)
	}
}

// outputState 概括一次模型调用的输出里终稿验收要用的部分。
type outputState struct {
	hitLimit  bool // 输出达到上限被截断（协议的结束原因写明，llm.HitOutputLimit）
	outTokens int  // 本次输出的 token 数（含思考）；适配器没给用量时为 0
}

// finalDraftProblem 判定模型自己停下时写下的终稿能不能原样交付：能返回空串，不能返回要模型
// 重做的原因（随收尾请求交还）。终稿是被输出上限截断的，原因就是截断：验收不通过的直接
// 原因（JSON 停在半截）只是它的结果。只说「取不出 JSON」，模型不知道上一版是被截断的、
// 该写短些。验收通过的被截断终稿照常交付：砍掉的只是 JSON 之后的说明。
func (r *reactRuntime) finalDraftProblem(config *ReActConfig, answer string) string {
	if !r.lastOutput.hitLimit {
		return config.checkFinalAnswer(answer)
	}
	if config.OutputCheck == nil || config.OutputCheck(answer) == nil {
		return ""
	}
	return fmt.Sprintf("上一版输出达到输出上限%s被截断，没有写完。请精简说明文字，把输出写完整。", r.lastOutput.limitNote())
}

// limitNote 是实际输出到了多少 token 的人读写法；没有用量时留空。
func (o outputState) limitNote() string {
	if o.outTokens > 0 {
		return fmt.Sprintf("（%d token）", o.outTokens)
	}
	return ""
}
