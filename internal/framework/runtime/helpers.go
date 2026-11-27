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
// 消息是它的回复）才有终稿：验收通过就原样交付，不通过就带着原因请模型再交一次。循环
// 在模型交出终稿之前被终止的（中间件止损、外部中止——此时最后一条是工具结果或中止
// 说明），没有终稿可交付，请模型基于已经做完的事收尾：执行者的观察、评估者的判定、
// 规划者的动作都写在这份终稿里，少了它整段执行白做。
func (r *reactRuntime) handleNormalCompletion(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) {
	why := whyNoFinalAnswer
	if last := result.MessageHistory[len(result.MessageHistory)-1]; last.Role == llm.RoleAssistant {
		why = config.checkFinalAnswer(last.Content)
	}
	if why == "" {
		return
	}
	if err := r.forceFinalAnswer(ctx, config, result, why); err != nil {
		result.InternalError = fmt.Errorf("force final answer failed: %w", err)
	}
}
