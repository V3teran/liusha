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
	// 加载初始历史
	if len(config.InitialHistory) > 0 {
		result.MessageHistory = append(result.MessageHistory, config.InitialHistory...)
	} else {
		r.mu.RLock()
		result.MessageHistory = append(result.MessageHistory, r.messageHistory...)
		r.mu.RUnlock()
	}

	// 添加系统提示
	if config.SystemPrompt != "" {
		result.AddSystemMessage(config.SystemPrompt)
	}

	// 添加用户目标
	result.AddUserMessage(config.Objective)
}

// executeIteration 执行单次迭代
func (r *DefaultReActRuntime) executeIteration(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
	iteration int,
) (*IterationTrace, bool, error) {
	// 执行单次迭代
	trace, shouldStop, err := r.runIteration(ctx, config, result, iteration)
	result.Trace = append(result.Trace, trace)
	result.Iterations = iteration

	// 回调：迭代完成
	if config.OnIteration != nil {
		config.OnIteration(iteration, trace.Status)
	}

	return trace, shouldStop, err
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

// finalizeResult 完成结果处理
func (r *DefaultReActRuntime) finalizeResult(result *ReActResult) {
	// 提取最终答案
	result.FinalAnswer = result.ExtractFinalAnswer()

	// 保存消息历史到运行时
	r.mu.Lock()
	r.messageHistory = result.MessageHistory
	r.mu.Unlock()
}
