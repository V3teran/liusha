// Package executor 提供执行层的所有组件。
//
// 使用 ReActRuntime 替换手写 ReAct 循环
package executor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// Agent 是 ReAct 执行引擎（使用 ReActRuntime）
type Agent struct {
	provider         llm.Provider
	reactRuntime     runtime.ReActRuntime
	emitter          SSEEmitter // 可为 nil
	logger           zerolog.Logger
	world            KnowledgeGraphReader // 用于读取 metadata
	eventBus         EventBus             // 用于接收外部控制事件
	checkpointer     core.Checkpointer    // 框架级统一 Checkpoint 接口
	checkpointPolicy runtime.CheckpointPolicy
}

// NewAgent 构造 Executor Agent
func NewAgent(
	p llm.Provider,
	emitter SSEEmitter,
	logger zerolog.Logger,
	world KnowledgeGraphReader,
	checkpointer core.Checkpointer,
	checkpointPolicy runtime.CheckpointPolicy,
) *Agent {
	// 创建 ReAct 运行时
	reactRuntime := runtime.NewReActRuntime()

	// 注意：工具注册由外部完成
	// 这里只创建空的 runtime，工具由 Coordinator 注册

	// 默认 Checkpoint 策略：每 3 次迭代保存
	policy := checkpointPolicy
	if policy == nil && checkpointer != nil {
		policy = runtime.NewIterationCheckpointPolicy(3)
	}

	return &Agent{
		provider:         p,
		reactRuntime:     reactRuntime,
		emitter:          emitter,
		logger:           logger,
		world:            world,
		eventBus:         nil, // 默认无事件总线
		checkpointer:     checkpointer,
		checkpointPolicy: policy,
	}
}

// RegisterTools 注册工具（供外部调用）
func (a *Agent) RegisterTools(tools []core.Tool) error {
	for _, tool := range tools {
		if err := a.reactRuntime.RegisterTool(tool); err != nil {
			return fmt.Errorf("register tool %s: %w", tool.Name(), err)
		}
	}
	return nil
}

// WithEventBus 配置事件总线
func (a *Agent) WithEventBus(bus EventBus) *Agent {
	a.eventBus = bus
	return a
}

// Run 执行 ReAct 循环（使用 ReActRuntime）
func (a *Agent) Run(ctx context.Context, actionID string, req ExecutorReq) (ExecutorResult, error) {
	a.logger.Info().
		Str("action_id", actionID).
		Int("max_steps", req.Budget.MaxSteps).
		Int("max_tokens", req.Budget.MaxTokens).
		Msg("[EXECUTOR] Run called with ReActRuntime")

	// 验证 Budget
	if req.Budget.MaxSteps <= 0 {
		a.logger.Warn().
			Str("action_id", actionID).
			Msg("[EXECUTOR] MaxSteps <= 0, using default budget")
		req.Budget = DefaultBudget()
	}

	// 构建 objective（从 req.Inbox 构建）
	objective := a.buildObjective(req)

	// 构建系统提示
	systemPrompt := req.System
	if systemPrompt == "" {
		systemPrompt = "你是一个渗透测试执行 Agent，负责执行具体的渗透测试任务。"
	}

	// 配置 ReAct 运行
	config := &runtime.ReActConfig{
		Objective:            objective,
		SystemPrompt:         systemPrompt,
		LLMProvider:          a.provider,
		ModelID:              "deepseek-chat",
		MaxIterations:        req.Budget.MaxSteps,
		Temperature:          0.7,
		MaxTokens:            req.Budget.MaxTokens,
		Tools:                a.reactRuntime.GetTools(),
		MessageModifierChain: runtime.NewExecutorModifierChain(),

		// Checkpoint 集成
		Checkpointer:     a.checkpointer,
		CheckpointPolicy: a.checkpointPolicy,
		TaskID:           actionID,

		OnIteration: func(iteration int, status runtime.IterationStatus) {
			a.logger.Info().
				Str("action_id", actionID).
				Int("iteration", iteration).
				Str("status", string(status)).
				Msg("[EXECUTOR] ReAct 迭代")

			// SSE 推送
			if a.emitter != nil {
				a.emitter.Emit(SSEEvent{
					Kind:     "thinking",
					ActionID: actionID,
					StepID:   iteration,
					Data:     map[string]interface{}{"status": status},
				})
			}
		},
	}

	// 运行 ReAct 循环
	startTime := time.Now()
	result, err := a.reactRuntime.Run(ctx, config)
	if err != nil {
		a.logger.Error().Err(err).Str("action_id", actionID).Msg("[EXECUTOR] ReAct runtime failed")
		return ExecutorResult{
			Steps:      []Step{},
			Halt:       HaltError,
			TokensUsed: 0,
		}, err
	}

	a.logger.Info().
		Str("action_id", actionID).
		Str("status", string(result.Status)).
		Int("iterations", result.Iterations).
		Int64("duration_ms", time.Since(startTime).Milliseconds()).
		Msg("[EXECUTOR] ReAct execution completed")

	// 转换结果
	execResult := a.convertResult(result)

	// SSE 推送完成事件
	if a.emitter != nil {
		a.emitter.Emit(SSEEvent{
			Kind:     "action_done",
			ActionID: actionID,
			Data:     execResult,
		})
	}

	return execResult, nil
}

// buildObjective 从请求构建 objective
func (a *Agent) buildObjective(req ExecutorReq) string {
	if len(req.Inbox) == 0 {
		return "请执行分配给你的任务。"
	}

	// 将 Inbox 消息组合为 objective
	return strings.Join(req.Inbox, "\n")
}

// convertResult 将 ReActResult 转换为 ExecutorResult
func (a *Agent) convertResult(result *runtime.ReActResult) ExecutorResult {
	// 构建 Steps
	steps := make([]Step, 0, result.Iterations)
	for i := 0; i < result.Iterations; i++ {
		steps = append(steps, Step{
			Index:   i,
			Thought: fmt.Sprintf("Iteration %d", i+1),
		})
	}

	// 确定 Halt 原因
	var halt HaltReason
	switch result.Status {
	case runtime.ReActStatusSuccess:
		halt = HaltDone
	case runtime.ReActStatusMaxIterations:
		halt = HaltBudget
	case runtime.ReActStatusCancelled:
		halt = HaltCancelled
	case runtime.ReActStatusError:
		halt = HaltError
	default:
		halt = HaltError
	}

	return ExecutorResult{
		Steps:      steps,
		Conclusion: result.FinalAnswer,
		Halt:       halt,
		TokensUsed: 0, // TODO: 从 result 提取 token 使用量
	}
}

// ============================================
// 类型定义在 types.go 中
// ============================================
