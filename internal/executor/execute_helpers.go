package executor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/registry"
)

// ActionData 是从 action.Content 解析出的数据
type ActionData struct {
	Type        string `json:"type"`
	Instruction string `json:"instruction"`
	Complexity  string `json:"complexity"`
	Host        string `json:"host"`
}

// parseActionContent 从 action 节点中解析内容
func (e *Engine) parseActionContent(action explorationgraph.Node) (*ActionData, error) {
	var data ActionData
	if err := json.Unmarshal(action.Content, &data); err != nil {
		return nil, fmt.Errorf("解析 Action 失败: %w", err)
	}
	return &data, nil
}

// prepareReActTools 准备并过滤 ReAct 工具列表
func (e *Engine) prepareReActTools() []registry.Tool {
	var tools []registry.Tool
	for _, t := range e.registry.WrappedTools() {
		if registry.Allows(e.functionTools, t.Name()) {
			tools = append(tools, t)
		}
	}
	return tools
}

// buildReActConfigWithMonitoring 构建带监控回调的 ReAct 配置
func (e *Engine) buildReActConfigWithMonitoring(
	ctx context.Context,
	action explorationgraph.Node,
	actionData *ActionData,
	tools []registry.Tool,
) (*runtime.ReActConfig, error) {
	// 构建执行目标
	objective := fmt.Sprintf("执行以下操作：%s\n\n类型：%s\n目标：%s",
		actionData.Instruction,
		actionData.Type,
		actionData.Host)

	// 构建系统提示
	systemPrompt := e.buildSystemPrompt(actionData.Type, actionData.Complexity, tools)

	// 获取 LLM Provider
	provider, err := e.router.For(ctx, e.complexity)
	if err != nil {
		return nil, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	// 构建配置，包含 monitor kill 检查
	config := &runtime.ReActConfig{
		Objective:            objective,
		SystemPrompt:         systemPrompt,
		LLMProvider:          provider,
		MaxIterations:        e.maxIt,
		Temperature:          0.7,
		MaxTokens:            4000,
		MessageModifierChain: runtime.NewDefaultModifierChain(15),
		TaskID:               action.TaskID,
		OnIteration: func(iteration int, status runtime.IterationStatus) {
			e.logger.Debug().
				Str("action_id", action.ID).
				Int("iteration", iteration).
				Str("status", string(status)).
				Msg("ReAct 迭代")

			// 每轮迭代检查 action 状态（Monitor kill_action 中断机制）
			if e.graph != nil {
				node, err := e.graph.GetNode(ctx, action.ID)
				if err == nil && node.State != nil && *node.State == explorationgraph.StateAborted {
					reason := "unknown"
					if node.BlockedReason != nil {
						reason = *node.BlockedReason
					}
					e.logger.Warn().
						Str("action_id", action.ID).
						Str("reason", reason).
						Msg("检测到 action 已被 monitor kill，中断执行")
				}
			}
		},
	}

	return config, nil
}

// checkActionAborted 检查 action 是否被 monitor kill
func (e *Engine) checkActionAborted(ctx context.Context, actionID string) error {
	if e.graph == nil {
		return nil
	}

	node, err := e.graph.GetNode(ctx, actionID)
	if err != nil {
		return nil // 忽略查询错误
	}

	if node.State != nil && *node.State == explorationgraph.StateAborted {
		reason := "unknown"
		if node.BlockedReason != nil {
			reason = *node.BlockedReason
		}
		e.logger.Warn().
			Str("action_id", actionID).
			Str("reason", reason).
			Msg("action 已被 monitor kill，提前返回（图层 CAS 已保护状态）")
		return fmt.Errorf("action killed by monitor: %s", reason)
	}

	return nil
}

// registerReActTools 注册工具到 ReAct runtime
func (e *Engine) registerReActTools(
	reactRuntime runtime.ReActRuntime,
	tools []registry.Tool,
	actionID string,
) error {
	e.logger.Info().
		Int("tool_count", len(tools)).
		Str("action_id", actionID).
		Msg("🔧 开始注册工具到 ReAct runtime")

	if len(tools) == 0 {
		e.logger.Error().
			Str("action_id", actionID).
			Msg("❌ 严重错误：没有可用工具！Executor 无法执行任何操作")
		// 仍然继续执行，但会失败
	}

	for _, tool := range tools {
		e.logger.Debug().
			Str("tool_name", tool.Name()).
			Str("action_id", actionID).
			Msg("注册工具")

		if err := reactRuntime.RegisterTool(tool); err != nil {
			return fmt.Errorf("注册工具 %s 失败: %w", tool.Name(), err)
		}
	}

	return nil
}

// executeReActRuntime 执行 ReAct runtime
func (e *Engine) executeReActRuntime(
	ctx context.Context,
	reactRuntime runtime.ReActRuntime,
	config *runtime.ReActConfig,
	actionID string,
) (*runtime.ReActResult, error) {
	e.logger.Info().
		Str("action_id", actionID).
		Msg("🚀 开始执行 ReAct")

	result, err := reactRuntime.Run(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("ReAct 执行失败: %w", err)
	}

	e.logger.Info().
		Str("action_id", actionID).
		Int("iterations", result.Iterations).
		Str("status", string(result.Status)).
		Msg("✅ ReAct 执行完成")

	return result, nil
}

// processExecutorOutput 处理 Executor 输出，创建 Observation 节点
func (e *Engine) processExecutorOutput(
	ctx context.Context,
	action explorationgraph.Node,
	result *runtime.ReActResult,
) ([]evaluator.Attempt, error) {
	// 解析输出
	output, err := ParseExecutorOutput(result)
	if err != nil {
		e.logger.Warn().
			Err(err).
			Str("action_id", action.ID).
			Msg("解析 Executor 输出失败，使用空输出")

		output = &ExecutorOutput{
			Status:       "completed",
			Summary:      "执行完成，但输出格式解析失败",
			Observations: []Observation{},
		}
	}

	e.logger.Info().
		Str("action_id", action.ID).
		Str("status", output.Status).
		Str("summary", output.Summary).
		Int("observations_count", len(output.Observations)).
		Msg("解析 Executor 输出")

	// 为每个观察结果创建 observation 节点
	var attempts []evaluator.Attempt
	for i, obs := range output.Observations {
		// 跳过没有 repro 的观察
		if len(obs.Repro) == 0 {
			e.logger.Warn().
				Str("action_id", action.ID).
				Int("observation_index", i).
				Str("statement", obs.Statement).
				Msg("跳过没有 repro 的观察")
			continue
		}

		// 创建 observation 节点
		node, err := e.createObservationNode(ctx, action.TaskID, action.ID, obs)
		if err != nil {
			e.logger.Error().
				Err(err).
				Str("action_id", action.ID).
				Int("observation_index", i).
				Msg("创建 observation 节点失败")
			continue
		}

		e.logger.Info().
			Str("action_id", action.ID).
			Str("observation_id", node.ID).
			Str("statement", obs.Statement).
			Msg("创建 observation 节点成功")

		// 转换为 Attempt
		attempt, err := e.observationToAttempt(action.TaskID, node, obs)
		if err != nil {
			e.logger.Error().
				Err(err).
				Str("observation_id", node.ID).
				Msg("转换 observation 为 Attempt 失败")
			continue
		}

		attempts = append(attempts, attempt)
	}

	e.logger.Info().
		Str("action_id", action.ID).
		Int("attempts_count", len(attempts)).
		Msg("Executor 输出处理完成")

	return attempts, nil
}
