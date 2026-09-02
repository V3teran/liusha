package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/eventbus"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// Agent 是事件驱动的 Planner Agent，通过 LLM 推理产出 Action
type Agent struct {
	taskID       string
	eventBus     *executor.PlannerEventBus  // Task 级事件总线（接收触发）
	actionBus    *eventbus.Bus        // Action 级事件总线（发送控制）
	world        *worldmodel.Store
	controlPlane *controlplane.Store
	router       *provider.Router
	tools        *ToolRegistry
	logger       zerolog.Logger

	stopCh             chan struct{}
	initialPlanDoneCh  chan struct{}  // 初始规划完成信号
}

// Config 配置 Planner Agent
type Config struct {
	TaskID       string
	EventBus     *executor.PlannerEventBus // Task 级事件总线
	ActionBus    *eventbus.Bus       // Action 级事件总线
	World        *worldmodel.Store
	ControlPlane *controlplane.Store
	Router       *provider.Router
	Logger       zerolog.Logger
}

// New 创建新的 Planner Agent
func New(cfg Config) *Agent {
	tools := NewToolRegistry()
	tools.Register(NewObserveStateTool(cfg.World))
	tools.Register(NewProposeMovesTool(cfg.World))
	tools.Register(NewEvaluateProgressTool(cfg.World))

	return &Agent{
		taskID:             cfg.TaskID,
		eventBus:           cfg.EventBus,
		actionBus:          cfg.ActionBus,
		world:              cfg.World,
		controlPlane:       cfg.ControlPlane,
		router:             cfg.Router,
		tools:              tools,
		logger:             cfg.Logger,
		stopCh:             make(chan struct{}),
		initialPlanDoneCh:  make(chan struct{}),
	}
}

// Start 启动 Planner Agent 的事件循环（阻塞运行）
func (a *Agent) Start(ctx context.Context) error {
	a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent started")

	// 订阅事件
	events := a.eventBus.Subscribe(a.taskID)
	defer a.eventBus.Unsubscribe(a.taskID)

	// 全局评估定时器（6 分钟）
	evaluationTicker := time.NewTicker(6 * time.Minute)
	defer evaluationTicker.Stop()

	// 初始规划：关键路径，失败则中止任务
	// 与后续 replan 不同：初始 planning 是任务的前置条件，必须成功
	if err := a.performInitialPlanning(ctx); err != nil {
		// 通知失败（Executor 会看到 channel 关闭但没有成功标记）
		close(a.initialPlanDoneCh)
		return fmt.Errorf("initial planning failed, aborting task: %w", err)
	}

	// 通知初始规划成功
	close(a.initialPlanDoneCh)

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent stopped (context canceled)")
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent stopped")
			return nil

		case event := <-events:
			a.logger.Debug().
				Str("task_id", a.taskID).
				Str("event_type", string(event.Type)).
				Msg("received event")

			if err := a.replan(ctx, event); err != nil {
				a.logger.Error().Err(err).Str("event_type", string(event.Type)).Msg("replan failed")
			}

		case <-evaluationTicker.C:
			// 定期全局评估
			if err := a.periodicEvaluation(ctx); err != nil {
				a.logger.Error().Err(err).Msg("periodic evaluation failed")
			}
		}
	}
}

// performInitialPlanning 执行初始规划，带重试逻辑
//
// 设计要点：
//   - 初始规划是任务的前置条件，失败则任务无法执行
//   - 重试 3 次，指数退避（2s, 4s, 6s）
//   - 客户端错误（401, 403）不重试，立即失败
//   - 网络错误、限流、服务端错误会重试
func (a *Agent) performInitialPlanning(ctx context.Context) error {
	const maxAttempts = 3
	baseDelay := 2 * time.Second

	event := executor.Event{
		Type:   executor.EventTaskStarted,
		TaskID: a.taskID,
	}

	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		a.logger.Info().
			Int("attempt", attempt).
			Int("max_attempts", maxAttempts).
			Str("task_id", a.taskID).
			Msg("attempting initial planning")

		err := a.replan(ctx, event)

		if err == nil {
			a.logger.Info().
				Int("attempt", attempt).
				Str("task_id", a.taskID).
				Msg("initial planning succeeded")
			return nil
		}

		lastErr = err

		a.logger.Warn().
			Err(err).
			Int("attempt", attempt).
			Str("task_id", a.taskID).
			Msg("initial planning attempt failed")

		// 检查是否是明确的客户端错误（不应重试）
		if isClientError(err) {
			a.logger.Error().
				Err(err).
				Str("task_id", a.taskID).
				Msg("initial planning failed with client error (non-retriable)")
			return fmt.Errorf("initial planning failed with client error: %w", err)
		}

		// 还有重试机会
		if attempt < maxAttempts {
			// 指数退避
			delay := time.Duration(attempt) * baseDelay
			a.logger.Info().
				Dur("delay", delay).
				Str("task_id", a.taskID).
				Msg("retrying initial planning after backoff")

			select {
			case <-time.After(delay):
				// 继续下一次尝试
			case <-ctx.Done():
				return fmt.Errorf("context canceled during retry backoff: %w", ctx.Err())
			}
		}
	}

	return fmt.Errorf("initial planning failed after %d attempts: %w", maxAttempts, lastErr)
}

// isClientError 判断是否是客户端错误（不应重试的错误）
//
// 包括：
//   - 401 Unauthorized: API Key 无效
//   - 403 Forbidden: 权限不足
//   - 400 Bad Request: 请求格式错误
//   - context.Canceled: 用户主动取消
func isClientError(err error) bool {
	if err == nil {
		return false
	}

	// context 取消不应重试
	if errors.Is(err, context.Canceled) {
		return true
	}

	// 检查错误消息中的 HTTP 状态码
	// 注意：这是启发式判断，理想情况应该用类型断言
	errMsg := err.Error()
	return strings.Contains(errMsg, "401") ||
		strings.Contains(errMsg, "403") ||
		strings.Contains(errMsg, "400")
}

// Stop 停止 Planner Agent
func (a *Agent) Stop() {
	close(a.stopCh)
}

// WaitInitialPlanDone 等待初始规划完成
func (a *Agent) WaitInitialPlanDone() <-chan struct{} {
	return a.initialPlanDoneCh
}

// periodicEvaluation 执行定期全局评估（每 6 分钟）。
func (a *Agent) periodicEvaluation(ctx context.Context) error {
	a.logger.Info().Str("task_id", a.taskID).Msg("starting periodic evaluation")

	// 1. 获取全局状态
	state, err := a.getGlobalState(ctx, a.taskID)
	if err != nil {
		return fmt.Errorf("get global state: %w", err)
	}

	// 2. LLM 全局评估
	assessment, err := a.evaluateGlobal(ctx, state)
	if err != nil {
		return fmt.Errorf("evaluate global: %w", err)
	}

	a.logger.Info().
		Str("strategy", assessment.Strategy).
		Int("kills", len(assessment.ActionsToKill)).
		Int("steers", len(assessment.ActionsToSteer)).
		Int("new_actions", len(assessment.NewActions)).
		Msg("evaluation completed")

	// 3. 执行决策（Kill/Steer/CreateAction）
	if err := a.executeDecisions(ctx, a.taskID, assessment); err != nil {
		return fmt.Errorf("execute decisions: %w", err)
	}

	return nil
}

// replan 执行重新规划（调用 LLM）
func (a *Agent) replan(ctx context.Context, event executor.Event) error {
	startTime := time.Now()

	// 构建系统提示词
	systemPrompt := a.buildSystemPrompt()

	// 构建用户提示词（包含当前状态和事件）
	userPrompt, err := a.buildUserPrompt(ctx, event)
	if err != nil {
		return fmt.Errorf("build user prompt: %w", err)
	}

	// 初始消息
	messages := []map[string]interface{}{
		{"role": "user", "content": userPrompt},
	}

	// 工具调用循环（最多 5 轮）
	maxRounds := 5
	for round := 0; round < maxRounds; round++ {
		// 调用 LLM
		a.logger.Info().
			Str("task_id", a.taskID).
			Int("round", round+1).
			Int("max_rounds", maxRounds).
			Msg("[PLANNER] Calling LLM")

		response, err := a.invokeRouter(ctx, systemPrompt, messages)
		if err != nil {
			return fmt.Errorf("invoke LLM (round %d): %w", round+1, err)
		}

		a.logger.Info().
			Str("task_id", a.taskID).
			Int("round", round+1).
			Msg("[PLANNER] LLM returned")

		// 将 LLM 响应加入消息历史
		messages = append(messages, response)

		// 提取工具调用
		toolCalls := a.extractToolCalls(response)

		a.logger.Info().
			Str("task_id", a.taskID).
			Int("round", round+1).
			Int("tool_calls_count", len(toolCalls)).
			Msg("[PLANNER] Extracted tool calls")

		if len(toolCalls) == 0 {
			// 无工具调用，LLM 完成规划
			a.logger.Info().
				Str("task_id", a.taskID).
				Int("rounds", round+1).
				Dur("duration", time.Since(startTime)).
				Msg("planning completed")
			return nil
		}

		// 执行工具调用
		a.logger.Info().
			Str("task_id", a.taskID).
			Int("round", round+1).
			Int("tool_calls_count", len(toolCalls)).
			Msg("[PLANNER] Executing tools")

		toolResults := make([]map[string]interface{}, 0, len(toolCalls))
		for i, tc := range toolCalls {
			a.logger.Info().
				Str("task_id", a.taskID).
				Int("round", round+1).
				Int("tool_index", i).
				Str("tool_name", tc.Name).
				Msg("[PLANNER] Executing tool")

			result, err := a.executeTool(ctx, tc)
			if err != nil {
				a.logger.Error().Err(err).Str("tool", tc.Name).Msg("tool execution failed")
				result = map[string]interface{}{
					"error": err.Error(),
				}
			}

			toolResults = append(toolResults, map[string]interface{}{
				"type":       "tool_result",
				"tool_use_id": tc.ID,
				"content":    result,
			})
		}

		// 将工具结果加入消息历史
		messages = append(messages, map[string]interface{}{
			"role":    "user",
			"content": toolResults,
		})
	}

	a.logger.Warn().
		Str("task_id", a.taskID).
		Msg("planning reached max rounds without completion")
	return nil
}

// buildSystemPrompt 构建系统提示词
func (a *Agent) buildSystemPrompt() string {
	return `你是一个安全测试规划 Agent。你的职责是：

1. 观察世界模型：当前已知的目标、观察记录和重要发现
2. 评估任务进展：判断目标是否达成、还有哪些未探索的方向
3. 生成执行计划：决定下一步执行哪些 Move，以及它们的优先级和复杂度

## 可用工具

### observe_state
观察当前世界模型状态（目标、Move、观察、发现）

### propose_moves
生成新的 Move。每个 Move 包含：
- instruction: 自然语言描述要做什么（必填）
- complexity: 执行复杂度（必填）
  - trivial: 极简任务（<5 步，快速查询）
  - simple: 简单任务（~10 步，信息收集）
  - moderate: 中等任务（~30 步，漏洞测试）
  - complex: 复杂任务（~50 步，漏洞利用）
  - extreme: 极限任务（~100 步，深度分析）
- target_ref: 目标定位（可选）
  - domain: web/binary/cloud/network/lateral
  - ref_kind: endpoint/file/host/port/service
  - locator: URL/路径/IP
- priority: 优先级 1-10（必填）
- depends_on: 依赖的其他 Move ID 列表（可选）

### evaluate_progress
评估任务进展，判断是否应该继续生成 Move

## 规划原则

1. **渐进式探索**：从简单到复杂，先信息收集再漏洞利用
2. **依赖关系**：某些 Move 依赖前置条件（例如：利用漏洞依赖已发现漏洞）
3. **避免重复**：不要重复执行已完成的 Move
4. **资源效率**：每次规划生成 1-5 个 Move，不要过多
5. **优先级排序**：高价值目标优先（漏洞利用 > 漏洞测试 > 信息收集）

## 输出格式

使用工具调用输出，不要输出纯文本解释。`

}

// buildUserPrompt 构建用户提示词
func (a *Agent) buildUserPrompt(ctx context.Context, event executor.Event) (string, error) {
	prompt := fmt.Sprintf("事件类型：%s\n\n", event.Type)

	switch event.Type {
	case executor.EventTaskStarted:
		prompt += "任务刚刚启动，请生成初始 Move。\n"
	case executor.EventActionCompleted:
		// 修复：字段名应为 action_id，不是 move_id
		actionID, ok := event.Payload["action_id"].(string)
		if !ok || actionID == "" {
			return "", fmt.Errorf("EventActionCompleted missing action_id in payload")
		}
		prompt += fmt.Sprintf("Action %s 已完成，请根据新状态重新规划。\n", actionID)
	case executor.EventVerificationPassed:
		nodeID, ok := event.Payload["node_id"].(string)
		if !ok || nodeID == "" {
			return "", fmt.Errorf("EventVerificationPassed missing node_id in payload")
		}
		prompt += fmt.Sprintf("节点 %s 验证通过，请根据新发现调整计划。\n", nodeID)
	case executor.EventManualGuidance:
		guidance, ok := event.Payload["guidance"].(string)
		if !ok {
			return "", fmt.Errorf("EventManualGuidance missing guidance in payload")
		}
		prompt += fmt.Sprintf("人工指导：%s\n", guidance)
	case executor.EventHeartbeat:
		prompt += "定期检查：评估当前进展，必要时生成新 Move。\n"
	}

	prompt += "\n请使用 observe_state 工具观察当前状态，然后决定下一步行动。"
	return prompt, nil
}

// invokeRouter 调用 LLM Router
func (a *Agent) invokeRouter(ctx context.Context, systemPrompt string, messages []map[string]interface{}) (map[string]interface{}, error) {
	// 获取 complex 模型（规划任务复杂度高）
	llm, err := a.router.For(ctx, "complex")
	if err != nil {
		return nil, fmt.Errorf("get complex model: %w", err)
	}

	// 转换消息格式
	providerMessages := make([]provider.Message, 0, len(messages)+1)
	providerMessages = append(providerMessages, provider.Message{
		Role:    provider.RoleUser,
		Content: systemPrompt,
	})

	for _, m := range messages {
		roleStr := m["role"].(string)
		var role provider.Role
		if roleStr == "assistant" {
			role = provider.RoleAssistant
		} else {
			role = provider.RoleUser
		}

		content := m["content"]

		// 处理不同类型的 content
		var contentStr string
		switch v := content.(type) {
		case string:
			contentStr = v
		case []interface{}:
			// 工具结果列表
			b, _ := json.Marshal(v)
			contentStr = string(b)
		default:
			b, _ := json.Marshal(v)
			contentStr = string(b)
		}

		providerMessages = append(providerMessages, provider.Message{
			Role:    role,
			Content: contentStr,
		})
	}

	// 构建工具 schema
	toolSchemas := a.tools.Schemas()

	// 调用 LLM
	req := provider.Request{
		Messages:  providerMessages,
		Tools:     toolSchemas,
		MaxTokens: 8000,
	}

	resp, err := llm.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm complete: %w", err)
	}

	// 转换响应
	return a.convertResponse(resp), nil
}

// extractToolCalls 从响应中提取工具调用
func (a *Agent) extractToolCalls(response map[string]interface{}) []ToolCall {
	content, ok := response["content"]
	if !ok {
		return nil
	}

	switch v := content.(type) {
	case []interface{}:
		var calls []ToolCall
		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if m["type"] == "tool_use" {
				calls = append(calls, ToolCall{
					ID:    m["id"].(string),
					Name:  m["name"].(string),
					Input: m["input"].(map[string]interface{}),
				})
			}
		}
		return calls
	default:
		return nil
	}
}

// executeTool 执行工具调用
func (a *Agent) executeTool(ctx context.Context, tc ToolCall) (interface{}, error) {
	tool, ok := a.tools.Get(tc.Name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", tc.Name)
	}

	ctx = context.WithValue(ctx, "task_id", a.taskID)
	return tool.Execute(ctx, tc.Input)
}

// convertResponse 将 provider.Response 转换为通用格式
func (a *Agent) convertResponse(resp provider.Response) map[string]interface{} {
	result := map[string]interface{}{
		"role": "assistant",
	}

	if len(resp.ToolCalls) > 0 {
		var content []interface{}
		for _, tc := range resp.ToolCalls {
			var input map[string]interface{}
			_ = json.Unmarshal(tc.Arguments, &input)

			content = append(content, map[string]interface{}{
				"type":  "tool_use",
				"id":    tc.ID,
				"name":  tc.Name,
				"input": input,
			})
		}
		result["content"] = content
	} else {
		result["content"] = resp.Content
	}

	return result
}

// ToolCall 是工具调用
type ToolCall struct {
	ID    string
	Name  string
	Input map[string]interface{}
}
