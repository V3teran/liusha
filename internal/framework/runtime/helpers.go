package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// initializeResult 初始化 ReActResult
func initializeResult() *ReActResult {
	return &ReActResult{
		MessageHistory: make([]llm.Message, 0),
		Trace:          make([]*IterationTrace, 0),
		Status:         ReActStatusSuccess,
		CheckpointIDs:  make([]core.CheckpointID, 0),
	}
}

// restoreFromCheckpoint 从检查点恢复状态
func (r *DefaultReActRuntime) restoreFromCheckpoint(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) (int, error) {
	if config.RestoreFromCheckpoint == "" || config.Checkpointer == nil {
		return 1, nil
	}

	checkpoint, err := config.Checkpointer.Load(ctx, config.RestoreFromCheckpoint)
	if err != nil {
		return 0, fmt.Errorf("load checkpoint failed: %w", err)
	}
	if checkpoint == nil {
		return 0, fmt.Errorf("load checkpoint failed: checkpoint %s not found", config.RestoreFromCheckpoint)
	}

	// 反序列化状态
	var state struct {
		MessageHistory []llm.Message     `json:"message_history"`
		Trace          []*IterationTrace `json:"trace"`
		Iteration      int               `json:"iteration"`
	}
	if err := json.Unmarshal(checkpoint.StateSnapshot, &state); err != nil {
		return 0, fmt.Errorf("deserialize checkpoint state failed: %w", err)
	}

	// 恢复消息历史和轨迹
	result.MessageHistory = state.MessageHistory
	result.Trace = state.Trace
	result.RestoredFromCheckpoint = config.RestoreFromCheckpoint
	result.RestoredIteration = state.Iteration

	// 从下一次迭代继续
	return state.Iteration + 1, nil
}

// initializeNewExecution 初始化新执行的消息历史
func (r *DefaultReActRuntime) initializeNewExecution(config *ReActConfig, result *ReActResult) {
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

// handleCheckpoint 处理检查点保存
func (r *DefaultReActRuntime) handleCheckpoint(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
	iteration int,
	trace *IterationTrace,
) {
	if config.Checkpointer == nil || config.CheckpointPolicy == nil {
		return
	}

	if !config.CheckpointPolicy.ShouldSave(iteration, trace) {
		return
	}

	cpID, err := r.saveCheckpoint(ctx, config, result, iteration)
	if err != nil {
		// 保存失败不中断执行；错误暴露在结果上供调用方审计
		result.LastCheckpointError = err
	} else {
		result.CheckpointID = cpID
		result.CheckpointIDs = append(result.CheckpointIDs, cpID)
	}
}

// handleMaxIterationsReached 处理达到最大迭代次数的情况
func (r *DefaultReActRuntime) handleMaxIterationsReached(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) {
	// 强制要求 LLM 输出最终总结
	if err := r.forceFinalSummary(ctx, config, result); err != nil {
		// 强制总结失败不算致命错误，继续处理
		result.LastCheckpointError = fmt.Errorf("force final summary failed: %w", err)
	}
}

// handleNormalCompletion 处理正常完成（LLM停止调用工具）的情况
// 确保输出格式符合下游解析器的期望
func (r *DefaultReActRuntime) handleNormalCompletion(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
) {
	// 检查最后一条助手消息是否已经是结构化JSON格式
	if len(result.MessageHistory) > 0 {
		lastMsg := result.MessageHistory[len(result.MessageHistory)-1]
		if lastMsg.Role == llm.RoleAssistant && isStructuredOutput(lastMsg.Content) {
			// 已经是结构化输出，无需再次处理
			return
		}
	}

	// LLM自然停止但未输出结构化格式，强制要求输出JSON
	if err := r.forceStructuredOutput(ctx, config, result); err != nil {
		result.LastCheckpointError = fmt.Errorf("force structured output failed: %w", err)
	}
}

// isStructuredOutput 检查内容是否已经是结构化JSON输出
func isStructuredOutput(content string) bool {
	// 简单检查：包含 "observations" 字段的JSON对象
	return len(content) > 0 &&
		(content[0] == '{' ||
			(len(content) > 7 && content[0:7] == "```json"))
}
