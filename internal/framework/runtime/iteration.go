package runtime

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// runIteration 运行单次迭代。
// 返回：(执行轨迹, 是否应该停止, 错误)
func (r *reactRuntime) runIteration(
	ctx context.Context,
	config *ReActConfig,
	result *ReActResult,
	iteration int,
) (*IterationTrace, bool, error) {
	trace := &IterationTrace{
		Iteration:    iteration,
		StartTime:    time.Now().UnixMilli(),
		Status:       IterationStatusRunning,
		Actions:      make([]*Action, 0),
		Observations: make([]string, 0),
	}

	// 0. 中间件：BeforeIteration
	if config.MiddlewareChain != nil {
		state := r.buildIterationState(iteration, result, trace)
		signal, err := config.MiddlewareChain.BeforeIteration(ctx, state)
		if err != nil {
			trace.Status = IterationStatusFailed
			trace.EndTime = time.Now().UnixMilli()
			return trace, false, fmt.Errorf("中间件 BeforeIteration 失败: %w", err)
		}
		if signal != nil && signal.ShouldStop {
			trace.Status = IterationStatusCompleted
			trace.EndTime = time.Now().UnixMilli()
			trace.Thought = fmt.Sprintf("中间件终止: %s - %s", signal.Reason, signal.Message)
			return trace, true, nil
		}
	}

	// 1. 调用 LLM（Thought + Action）——外部中止在此先行检查：长生成期间
	//    的止血不等迭代边界。
	if config.AbortCheck != nil && config.AbortCheck() {
		result.AddUserMessage("（外部中止：本轮执行已被裁决终止，停止后续调用）")
		trace.Status = IterationStatusCompleted
		trace.EndTime = time.Now().UnixMilli()
		return trace, true, nil
	}
	response, err := r.callLLM(ctx, config, result.MessageHistory)
	if err != nil {
		trace.Status = IterationStatusFailed
		trace.EndTime = time.Now().UnixMilli()
		return trace, false, fmt.Errorf("LLM 调用失败: %w", err)
	}

	// token 用量逐迭代累计（时间线每步用量 chip 的数据源）。
	result.Usage = result.Usage.Add(response.Usage)

	// 提取思考内容
	trace.Thought = response.Content

	// 回调：思考
	if config.OnThought != nil && response.Content != "" {
		config.OnThought(response.Content)
	}

	// 检查提前终止条件
	if config.EarlyStopCondition != nil && config.EarlyStopCondition(response.Content) {
		trace.Status = IterationStatusCompleted
		trace.EndTime = time.Now().UnixMilli()

		// 添加助手消息
		result.AddAssistantMessage(response.Content, nil, response.Thinking)
		result.FinalThinking = thinkingText(response.Thinking)

		r.finishIteration(ctx, config, iteration, result, trace)

		return trace, true, nil
	}

	// 2. 检查是否有工具调用
	if len(response.ToolCalls) == 0 {
		// 空响应不是完成：正文与工具调用双空的助手轮次对 ReAct 协议零信息——
		// 几乎总是生成预算被思考过程耗尽后截断（finish_reason=length 的常见
		// 形态）。接受它会让本轮以空 FinalAnswer 收场、调用方把已完成的迭代
		// 全部作废；这里改为注入纠偏指令继续循环，预算压力随上下文重排自然缓解。
		if strings.TrimSpace(response.Content) == "" {
			r.consecutiveEmptyResponses++
			if r.consecutiveEmptyResponses >= 2 {
				result.AddAssistantMessage(response.Content, nil, response.Thinking)
				trace.Status = IterationStatusFailed
				trace.EndTime = time.Now().UnixMilli()
				return trace, false, fmt.Errorf("连续 %d 次空响应（生成截断或模型退化）", r.consecutiveEmptyResponses)
			}
			result.AddAssistantMessage(response.Content, nil, response.Thinking)
			result.AddUserMessage("（上一条响应正文为空——生成被截断。请直接输出最终结果正文，不要展开新的思考过程）")
			trace.Status = IterationStatusFailed
			trace.EndTime = time.Now().UnixMilli()
			return trace, false, nil
		}
		r.consecutiveEmptyResponses = 0

		// 没有工具调用 = 得出最终答案
		trace.Status = IterationStatusCompleted
		trace.EndTime = time.Now().UnixMilli()

		// 添加助手消息
		result.AddAssistantMessage(response.Content, nil, response.Thinking)
		result.FinalThinking = thinkingText(response.Thinking)

		r.finishIteration(ctx, config, iteration, result, trace)

		return trace, true, nil
	}

	// 2.5 工具调用轮次（无工具调用的路径已在上方 return）：
	// 有效轮次清空连续空响应计数——空响应止损只统计真正连续的空转。
	r.consecutiveEmptyResponses = 0
	// 添加助手消息（包含工具调用；thinking 随消息携带供多轮 tool_use 规范回传）
	result.AddAssistantMessage(response.Content, response.ToolCalls, response.Thinking)

	// 3. 执行工具调用（Observation）——每个工具执行前同样检查外部中止：
	//    浏览器/长命令执行期间的 kill 即刻生效。
	for _, toolCall := range response.ToolCalls {
		if config.AbortCheck != nil && config.AbortCheck() {
			result.AddUserMessage("（外部中止：本轮执行已被裁决终止，跳过剩余工具调用）")
			trace.Status = IterationStatusCompleted
			trace.EndTime = time.Now().UnixMilli()
			return trace, true, nil
		}
		action := &Action{
			ToolCall:  toolCall,
			Thought:   response.Content,
			Timestamp: time.Now().UnixMilli(),
		}
		trace.Actions = append(trace.Actions, action)

		// 回调：动作
		if config.OnAction != nil {
			config.OnAction(action)
		}

		// 执行工具
		observation, failed, err := r.executeTool(ctx, toolCall)
		if err != nil {
			observation = fmt.Sprintf("工具执行失败: %s", err.Error())
			failed = true
		}
		if failed {
			r.consecutiveToolFailures++
		} else {
			r.consecutiveToolFailures = 0
		}

		trace.Observations = append(trace.Observations, observation)

		// 回调：观察
		if config.OnObservation != nil {
			config.OnObservation(observation)
		}

		// 添加工具结果消息
		result.AddToolMessage(toolCall.ID, observation)
	}

	// 4. 连续工具失败止损：环境有障碍时 LLM 会反复换姿势重试同一堵墙，
	// 及时终止把已收集的观察交回上层，防止无限挣扎。
	if limit := r.toolFailureLimit(config); limit > 0 && r.consecutiveToolFailures >= limit {
		summary := fmt.Sprintf(
			"（连续 %d 次工具执行失败，达到止损阈值 %d——停止重试。请基于已收集的观察给出结论）",
			r.consecutiveToolFailures, limit)
		result.AddUserMessage(summary)
		r.consecutiveToolFailures = 0
		trace.Status = IterationStatusCompleted
		trace.EndTime = time.Now().UnixMilli()

		r.finishIteration(ctx, config, iteration, result, trace)

		return trace, true, nil
	}

	trace.Status = IterationStatusCompleted
	trace.EndTime = time.Now().UnixMilli()

	// 5. 中间件：AfterIteration
	if config.MiddlewareChain != nil {
		state := r.buildIterationState(iteration, result, trace)
		signal, err := config.MiddlewareChain.AfterIteration(ctx, state)
		if err != nil {
			return trace, false, fmt.Errorf("中间件 AfterIteration 失败: %w", err)
		}
		if signal != nil && signal.ShouldStop {
			// 中间件要求停止，添加说明消息
			summary := fmt.Sprintf(
				"（中间件检测到异常：%s - %s，终止执行）",
				signal.Reason, signal.Message)
			result.AddUserMessage(summary)
			return trace, true, nil
		}
	}

	return trace, false, nil
}

// finishIteration 触发本轮 AfterIteration 中间件。三种「本轮已定局」路径
// （提前终止 / 最终答案 / 工具失败止损）共用：停止信号对本轮无意义，
// 收尾错误不阻断已定的完成态。
func (r *reactRuntime) finishIteration(
	ctx context.Context,
	config *ReActConfig,
	iteration int,
	result *ReActResult,
	trace *IterationTrace,
) {
	if config.MiddlewareChain == nil {
		return
	}
	state := r.buildIterationState(iteration, result, trace)
	_, _ = config.MiddlewareChain.AfterIteration(ctx, state)
}

// buildIterationState 构建迭代状态快照供中间件使用。
func (r *reactRuntime) buildIterationState(
	iteration int,
	result *ReActResult,
	trace *IterationTrace,
) *IterationState {
	// OutputCount 取累计观察数（含当前迭代）：工具还在产出输出 = 有进展，
	// 进度中间件的「无新输出」检测据此复位——此前该字段无人填充，检测
	// 永远看不见进展，退化成隐形的迭代硬顶（长而有效的 ReAct 被误判腰斩）。
	outputCount := len(trace.Observations)
	for _, t := range result.Trace {
		outputCount += len(t.Observations)
	}
	state := &IterationState{
		Iteration:               iteration,
		Thought:                 trace.Thought,
		ToolCalls:               trace.Actions,
		ConsecutiveToolFailures: r.consecutiveToolFailures,
		IterationSucceeded:      trace.Status == IterationStatusCompleted,
		OutputCount:             outputCount,
		Metadata:                make(map[string]interface{}),
	}

	// 提取最近的工具名称（最近 20 次）
	recentToolNames := make([]string, 0, 20)
	for i := len(result.Trace) - 1; i >= 0 && len(recentToolNames) < 20; i-- {
		for _, action := range result.Trace[i].Actions {
			if action.ToolCall.Name != "" {
				recentToolNames = append(recentToolNames, action.ToolCall.Name)
				if len(recentToolNames) >= 20 {
					break
				}
			}
		}
	}
	// 加上当前迭代的工具
	for _, action := range trace.Actions {
		if action.ToolCall.Name != "" {
			recentToolNames = append(recentToolNames, action.ToolCall.Name)
		}
	}
	state.RecentToolNames = recentToolNames

	// 提取最近的思考内容（最近 10 次）
	recentThoughts := make([]string, 0, 10)
	for i := len(result.Trace) - 1; i >= 0 && len(recentThoughts) < 10; i-- {
		if result.Trace[i].Thought != "" {
			recentThoughts = append(recentThoughts, result.Trace[i].Thought)
		}
	}
	// 加上当前思考
	if trace.Thought != "" {
		recentThoughts = append(recentThoughts, trace.Thought)
	}
	state.RecentThoughts = recentThoughts

	// 最后的观察
	if len(trace.Observations) > 0 {
		state.LastObservation = trace.Observations[len(trace.Observations)-1]
	}

	return state
}
