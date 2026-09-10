package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agentcore"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/eventbus"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// Agent 是事件驱动的 Planner Agent，通过 LLM 推理产出 Action
type Agent struct {
	core         *agentcore.Agent          // 统一 Agent 框架（封装 LLM 循环）
	taskID       string
	eventBus     *executor.PlannerEventBus // Task 级事件总线（接收触发）
	actionBus    *eventbus.Bus             // Action 级事件总线（发送控制）
	world        *knowledgegraph.Store
	controlPlane *controlplane.Store
	router       *provider.Router
	logger       zerolog.Logger

	stopCh            chan struct{}
	initialPlanDoneCh chan struct{} // 初始规划完成信号
}

// Config 配置 Planner Agent
type Config struct {
	TaskID       string
	EventBus     *executor.PlannerEventBus // Task 级事件总线
	ActionBus    *eventbus.Bus             // Action 级事件总线
	World        *knowledgegraph.Store
	ControlPlane *controlplane.Store
	Router       *provider.Router
	Logger       zerolog.Logger
}

// New 创建新的 Planner Agent
func New(cfg Config) *Agent {
	// 构建工具集
	reg := registry.New()

	// Roadmap 工具（核心规划机制）
	reg.Register(NewObserveRoadmapTool(cfg.World, cfg.Logger))
	reg.Register(NewGenerateRoadmapTool(cfg.World, cfg.Logger))

	// 世界模型观察工具（辅助）
	reg.Register(NewObserveStateTool(cfg.World))

	// 传统工具（兼容旧逻辑，后续可移除）
	reg.Register(NewProposeActionsTool(cfg.World, cfg.Logger))
	reg.Register(NewEvaluateProgressTool(cfg.World))

	// 创建 Agent 实例
	agent := &Agent{
		taskID:            cfg.TaskID,
		eventBus:          cfg.EventBus,
		actionBus:         cfg.ActionBus,
		world:             cfg.World,
		controlPlane:      cfg.ControlPlane,
		router:            cfg.Router,
		logger:            cfg.Logger,
		stopCh:            make(chan struct{}),
		initialPlanDoneCh: make(chan struct{}),
	}

	// 创建统一 Agent 核心
	core := agentcore.New(agentcore.Config{
		Name:         "planner",
		Provider:     nil, // 延迟设置（需要从 Router 获取）
		Registry:     reg,
		Logger:       cfg.Logger,
		MaxRounds:    100,
		MaxTokens:    8000,
		SystemPrompt: agent.buildSystemPrompt(),
	})

	agent.core = core

	return agent
}

// Start 启动 Planner Agent 的事件循环（阻塞运行）
//
// Planner 职责：纯规划
// - 响应事件（ActionCompleted, FindingVerified 等）
// - 调用 LLM 生成新的 Actions
// - 不负责监察（由 Monitor Agent 负责）
func (a *Agent) Start(ctx context.Context) error {
	a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent started")

	// 订阅事件
	events := a.eventBus.Subscribe(a.taskID)
	defer a.eventBus.Unsubscribe(a.taskID)

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

	// 工具调用循环（最多 100 轮）
	maxRounds := 100
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
				"type":        "tool_result",
				"tool_use_id": tc.ID,
				"content":     result,
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
	return `你是一个探索式任务规划 Agent。你的职责是：

1. 生成和维护任务的 Roadmap（路线图）
2. 根据执行结果动态调整 Roadmap
3. 确保探索式任务稳步推进

## 核心概念

### Roadmap（路线图）
- Roadmap 是任务的高层规划，由 10-15 个步骤组成
- 每个步骤是一个可验证的里程碑（中粒度目标）
- 步骤之间可以有依赖关系
- Roadmap 是动态的，会根据执行结果更新

### RoadmapStep（步骤）
- objective: 步骤目标（自然语言描述）
- step: 步骤编号（1.0, 2.0, ...，支持小数如 1.5）
- depends_on: 依赖的步骤编号（高层依赖）
- status: todo（待执行）/active（执行中）/complete（已完成）/skipped（已跳过）

### Action（动作）
- 从 RoadmapStep 派发的实际执行任务
- 一个 Step 可以派发多个 Action（1:N 映射）
- Action 有低层依赖（depends_on，限定在同一 Step 内）

## 可用工具

### observe_roadmap
观察当前任务的 Roadmap 状态

### generate_roadmap
生成或更新 Roadmap（完全替换式更新）

参数：
- steps: 完整的步骤列表（10-15 个步骤）
  - step: 步骤编号（1.0, 2.0, ...）
  - objective: 步骤目标（如"识别 Web 服务类型并测试常见漏洞"）
  - depends_on: 依赖的步骤编号（可选）
  - rationale: 为什么规划这一步（可选，用于调试）

### observe_state
观察当前世界模型状态（目标、Action、观察、发现）

### propose_actions（兼容旧逻辑，优先使用 Roadmap）
生成新的 Action

## 规划原则

1. **中粒度步骤**：每个步骤应该是可验证的里程碑，不要太粗（"信息收集"）也不要太细（"扫描 80 端口"）
   - 好的例子："端口扫描和服务识别"、"测试 SQL 注入"、"测试 XSS"
   - 不好的例子："信息收集"（太粗）、"curl http://target"（太细）

2. **探索式规划**：
   - 初始规划：生成粗略的 Roadmap（10-15 步）
   - 动态调整：根据执行结果，插入、修改或跳过步骤
   - 完全替换：每次更新时重新生成完整 Roadmap（简化 LLM 认知负担）

3. **依赖管理**：
   - 高层依赖：Step 2 依赖 Step 1（通过 RoadmapStep.depends_on）
   - 低层依赖：Action 之间的依赖（通过 Action.depends_on，限定在同一 Step 内）

4. **逐步推进**：
   - 初始规划后，只派发第一个 Step 的 Action
   - Step 完成后，被唤醒，派发下一个 Step
   - 根据发现动态调整后续步骤

5. **状态转换**：
   - Step: todo → active（派发了第一个 Action）
   - Step: active → complete（所有 Action 完成）
   - Step: todo/active → skipped（决定跳过）

## 工作流程

### 初始规划（任务启动时）
1. 使用 observe_state 观察任务目标
2. 使用 generate_roadmap 生成初始 Roadmap（10-15 步）
3. 使用 propose_actions 为第一个 Step 派发 Action

### 动态调整（Step 完成后）
1. 使用 observe_roadmap 查看当前 Roadmap
2. 使用 observe_state 查看执行结果和新发现
3. 根据结果调整 Roadmap：
   - 插入新步骤（如发现新攻击面）
   - 修改后续步骤（如改变目标）
   - 跳过不需要的步骤（如目标已达成）
4. 使用 generate_roadmap 保存更新后的 Roadmap
5. 使用 propose_actions 为下一个可执行的 Step 派发 Action

## 输出格式

使用工具调用输出，不要输出纯文本解释。优先使用 Roadmap 机制。`

}

// buildUserPrompt 构建用户提示词
func (a *Agent) buildUserPrompt(ctx context.Context, event executor.Event) (string, error) {
	prompt := fmt.Sprintf("事件类型：%s\n\n", event.Type)

	switch event.Type {
	case executor.EventTaskStarted:
		prompt += "任务刚刚启动，请生成初始 Action。\n"
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
		prompt += "定期检查：评估当前进展，必要时生成新 Action。\n"
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
	toolSchemas := a.core.Registry().Schemas()

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
	tool, ok := a.core.Registry().Get(tc.Name)
	if !ok {
		return nil, fmt.Errorf("unknown tool: %s", tc.Name)
	}

	// 将 map 转换为 json.RawMessage
	argsJSON, err := json.Marshal(tc.Input)
	if err != nil {
		return nil, fmt.Errorf("marshal tool args: %w", err)
	}

	ctx = context.WithValue(ctx, "task_id", a.taskID)
	result, err := tool.Execute(ctx, argsJSON)
	if err != nil {
		return nil, err
	}

	// 返回 ToolResult.Output
	return result.Output, nil
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
