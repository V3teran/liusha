package planneragent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/executionplan"
	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// Agent 是事件驱动的 Planner Agent，通过 LLM 推理产出 Move
type Agent struct {
	taskID       string
	eventBus     *cognition.EventBus
	planStore    *executionplan.Store
	world        *worldmodel.Store
	controlPlane *controlplane.Store
	router       *provider.Router
	tools        *ToolRegistry
	logger       zerolog.Logger

	// 控制循环
	stopCh chan struct{}
}

// Config 配置 Planner Agent
type Config struct {
	TaskID       string
	EventBus     *cognition.EventBus
	PlanStore    *executionplan.Store
	World        *worldmodel.Store
	Leads        *lead.Store
	ControlPlane *controlplane.Store
	Router       *provider.Router
	Logger       zerolog.Logger
}

// New 创建新的 Planner Agent
func New(cfg Config) *Agent {
	tools := NewToolRegistry()
	tools.Register(NewObserveStateTool(cfg.World))
	tools.Register(NewProposeMovesTool(cfg.PlanStore))
	tools.Register(NewEvaluateProgressTool(cfg.PlanStore, cfg.World))
	tools.Register(NewAccessMemoryTool(cfg.Leads, cfg.World))

	return &Agent{
		taskID:       cfg.TaskID,
		eventBus:     cfg.EventBus,
		planStore:    cfg.PlanStore,
		world:        cfg.World,
		controlPlane: cfg.ControlPlane,
		router:       cfg.Router,
		tools:        tools,
		logger:       cfg.Logger,
		stopCh:       make(chan struct{}),
	}
}

// Start 启动 Planner Agent 的事件循环（阻塞运行）
func (a *Agent) Start(ctx context.Context) error {
	a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent 启动")

	// 订阅事件
	events := a.eventBus.Subscribe(a.taskID)
	defer a.eventBus.Unsubscribe(a.taskID)

	// 心跳定时器
	heartbeatTicker := time.NewTicker(30 * time.Second)
	defer heartbeatTicker.Stop()

	// 初始规划
	if err := a.replan(ctx, cognition.Event{
		Type:   cognition.EventTaskStarted,
		TaskID: a.taskID,
	}); err != nil {
		a.logger.Error().Err(err).Msg("初始规划失败")
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent 停止（上下文取消）")
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Str("task_id", a.taskID).Msg("Planner Agent 停止")
			return nil

		case event := <-events:
			a.logger.Debug().
				Str("task_id", a.taskID).
				Str("event_type", string(event.Type)).
				Msg("收到事件，触发重新规划")

			if err := a.replan(ctx, event); err != nil {
				a.logger.Error().Err(err).Str("event_type", string(event.Type)).Msg("重新规划失败")
			}

		case <-heartbeatTicker.C:
			// 定期心跳，检查是否需要重新评估
			a.eventBus.PublishHeartbeat(a.taskID)
		}
	}
}

// Stop 停止 Planner Agent
func (a *Agent) Stop() {
	close(a.stopCh)
}

// replan 执行重新规划逻辑（调用 LLM）
func (a *Agent) replan(ctx context.Context, event cognition.Event) error {
	startTime := time.Now()

	// 构建系统提示词
	systemPrompt := a.buildSystemPrompt()

	// 构建用户提示词（包含当前状态和事件）
	userPrompt, err := a.buildUserPrompt(ctx, event)
	if err != nil {
		return fmt.Errorf("构建用户提示词: %w", err)
	}

	// 初始消息
	messages := []map[string]interface{}{
		{"role": "user", "content": userPrompt},
	}

	// 工具调用循环（最多 5 轮）
	maxRounds := 5
	for round := 0; round < maxRounds; round++ {
		// 调用 LLM
		response, err := a.invokeRouter(ctx, systemPrompt, messages)
		if err != nil {
			return fmt.Errorf("调用 LLM (round %d): %w", round+1, err)
		}

		// 将 LLM 响应加入消息历史
		messages = append(messages, response)

		// 提取工具调用
		toolCalls := a.extractToolCalls(response)
		if len(toolCalls) == 0 {
			// 没有工具调用，结束循环
			a.logger.Debug().Int("rounds", round+1).Msg("LLM 未返回工具调用，规划完成")
			break
		}

		// 执行所有工具调用，收集结果
		toolResults := make([]map[string]interface{}, 0, len(toolCalls))
		for _, call := range toolCalls {
			result, err := a.executeToolCall(ctx, call)
			if err != nil {
				a.logger.Error().Err(err).Str("tool", call.Name).Msg("工具执行失败")
				result = map[string]interface{}{
					"type":       "tool_result",
					"tool_use_id": call.ID,
					"is_error":   true,
					"content":    fmt.Sprintf("Error: %s", err.Error()),
				}
			} else {
				result["type"] = "tool_result"
				result["tool_use_id"] = call.ID
			}
			toolResults = append(toolResults, result)
		}

		// 将工具结果作为下一轮的 user 消息
		messages = append(messages, map[string]interface{}{
			"role":    "user",
			"content": toolResults,
		})
	}

	a.logger.Info().
		Str("task_id", a.taskID).
		Str("event_type", string(event.Type)).
		Dur("duration", time.Since(startTime)).
		Msg("重新规划完成")

	return nil
}

// ToolCall 表示一个工具调用请求
type ToolCall struct {
	ID    string
	Name  string
	Input map[string]interface{}
}

// extractToolCalls 从 LLM 响应中提取工具调用
func (a *Agent) extractToolCalls(response map[string]interface{}) []ToolCall {
	content, ok := response["content"].([]interface{})
	if !ok {
		return nil
	}

	var calls []ToolCall
	for _, item := range content {
		block, ok := item.(map[string]interface{})
		if !ok {
			continue
		}

		if block["type"] == "tool_use" {
			call := ToolCall{
				ID:   getString(block, "id"),
				Name: getString(block, "name"),
			}
			if input, ok := block["input"].(map[string]interface{}); ok {
				call.Input = input
			}
			calls = append(calls, call)
		}
	}

	return calls
}

// executeToolCall 执行单个工具调用
func (a *Agent) executeToolCall(ctx context.Context, call ToolCall) (map[string]interface{}, error) {
	inputJSON, err := json.Marshal(call.Input)
	if err != nil {
		return nil, fmt.Errorf("序列化工具输入: %w", err)
	}

	result, err := a.tools.ExecuteTool(ctx, call.Name, string(inputJSON))
	if err != nil {
		return nil, err
	}

	a.logger.Debug().
		Str("tool", call.Name).
		Interface("result", result).
		Msg("工具执行成功")

	// 将结果序列化为字符串（LLM 需要的格式）
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("序列化工具结果: %w", err)
	}

	return map[string]interface{}{
		"content": string(resultJSON),
	}, nil
}

// buildSystemPrompt 构建 Planner 的系统提示词
func (a *Agent) buildSystemPrompt() string {
	return `You are an autonomous penetration testing Planner Agent.

Your role is to:
1. Observe the current world model state (confirmed assets, credentials, access, findings)
2. Evaluate progress toward the goal
3. Propose the next exploration moves (enumerate/probe/exploit/escalate/persist)
4. Prioritize moves based on attack surface and potential impact

Key principles:
- Always propose moves with clear reasoning (audit trail)
- Prioritize high-value targets (credentials, admin access, sensitive data)
- Follow the kill chain: enumerate → probe → exploit → escalate → persist
- Be adaptive: re-evaluate when new findings emerge
- Ensure moves are executable by domain executors (web/binary/cloud/lateral)

Available tools:
- observe_state: Read world model (nodes & edges)
- propose_moves: Submit moves to execution queue
- evaluate_progress: Check task progress and decide if goal is met
- access_memory: Read working memory (leads, observations)

Always use tools to interact with the system. Do not output plain text responses.`
}

// buildUserPrompt 构建当前状态和事件的提示词
func (a *Agent) buildUserPrompt(ctx context.Context, event cognition.Event) (string, error) {
	// 查询当前待执行的 Move
	pendingMoves, err := a.planStore.ListPending(ctx, a.taskID)
	if err != nil {
		return "", fmt.Errorf("查询待执行 Move: %w", err)
	}

	prompt := fmt.Sprintf(`# Event Triggered
Type: %s
Timestamp: %s

# Current State
- Pending moves: %d

# Task
Task ID: %s

`,
		event.Type,
		event.Timestamp.Format(time.RFC3339),
		len(pendingMoves),
		a.taskID,
	)

	// 如果是 ManualGuidance 事件，读取控制事件
	if event.Type == cognition.EventManualGuidance && a.controlPlane != nil {
		controlEvents, err := a.controlPlane.ListPending(ctx, a.taskID)
		if err != nil {
			a.logger.Warn().Err(err).Msg("读取控制事件失败")
		} else if len(controlEvents) > 0 {
			prompt += "# Manual Guidance\nUser has issued control commands:\n\n"
			for _, ce := range controlEvents {
				prompt += fmt.Sprintf("- Command: %s\n", ce.Command)
				prompt += fmt.Sprintf("  Payload: %s\n", string(ce.Payload))
				prompt += fmt.Sprintf("  Created: %s\n\n", ce.CreatedAt.Format(time.RFC3339))

				// 标记为已处理
				if err := a.controlPlane.MarkProcessed(ctx, ce.ID); err != nil {
					a.logger.Error().Err(err).Str("event_id", ce.ID.String()).Msg("标记控制事件为已处理失败")
				}
			}
		}
	}

	prompt += `# Action Required
Based on the event and current state:
1. Use observe_state to see the latest world model
2. Use evaluate_progress to check if we're making progress
3. If needed, use propose_moves to add new exploration moves
4. Consider the event context when planning next steps

`

	return prompt, nil
}

// invokeRouter 调用 LLM Router 获取 Planner 的推理结果
func (a *Agent) invokeRouter(ctx context.Context, systemPrompt string, messages []map[string]interface{}) (map[string]interface{}, error) {
	// 获取 Planner tier 的 provider
	p, err := a.router.For(ctx, provider.ComplexityComplex)
	if err != nil {
		return nil, fmt.Errorf("resolve planner provider: %w", err)
	}

	// 转换消息格式
	providerMessages := a.convertMessages(systemPrompt, messages)

	// 构建工具定义
	toolSchemas := a.buildToolSchemas()

	// 构建请求
	req := provider.Request{
		Messages:  providerMessages,
		Tools:     toolSchemas,
		MaxTokens: 4096,
	}

	// 调用 LLM
	resp, err := p.Complete(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("llm complete: %w", err)
	}

	// 转换响应为通用格式
	return a.convertResponse(resp), nil
}

// convertMessages 将内部消息格式转换为 provider.Message
func (a *Agent) convertMessages(systemPrompt string, messages []map[string]interface{}) []provider.Message {
	var result []provider.Message

	// 添加 system 消息
	if systemPrompt != "" {
		result = append(result, provider.Message{
			Role:    provider.RoleSystem,
			Content: systemPrompt,
		})
	}

	// 转换其他消息
	for _, msg := range messages {
		role := getString(msg, "role")

		providerMsg := provider.Message{}
		switch role {
		case "user":
			providerMsg.Role = provider.RoleUser
		case "assistant":
			providerMsg.Role = provider.RoleAssistant
		default:
			continue
		}

		// 处理 content
		if content, ok := msg["content"].(string); ok {
			providerMsg.Content = content
		} else if contentList, ok := msg["content"].([]interface{}); ok {
			// 多模态内容或工具结果
			var parts []provider.ContentPart
			for _, item := range contentList {
				if itemMap, ok := item.(map[string]interface{}); ok {
					if itemMap["type"] == "tool_result" {
						// 工具结果转换为文本
						if resultContent, ok := itemMap["content"].(string); ok {
							parts = append(parts, provider.ContentPart{
								Type: "text",
								Text: resultContent,
							})
						}
					}
				}
			}
			if len(parts) > 0 {
				providerMsg.Parts = parts
			}
		}

		// 处理工具调用（assistant 消息可能包含）
		if role == "assistant" {
			if content, ok := msg["content"].([]interface{}); ok {
				var toolCalls []provider.ToolCall
				for _, item := range content {
					if itemMap, ok := item.(map[string]interface{}); ok {
						if itemMap["type"] == "tool_use" {
							inputJSON, _ := json.Marshal(itemMap["input"])
							toolCalls = append(toolCalls, provider.ToolCall{
								ID:        getString(itemMap, "id"),
								Name:      getString(itemMap, "name"),
								Arguments: json.RawMessage(inputJSON),
							})
						}
					}
				}
				if len(toolCalls) > 0 {
					providerMsg.ToolCalls = toolCalls
				}
			}
		}

		result = append(result, providerMsg)
	}

	return result
}

// buildToolSchemas 构建工具 schema
func (a *Agent) buildToolSchemas() []provider.ToolSchema {
	var schemas []provider.ToolSchema
	for _, tool := range a.tools.List() {
		schemaJSON, _ := json.Marshal(tool.Schema())
		schemas = append(schemas, provider.ToolSchema{
			Name:        tool.Name(),
			Description: tool.Description(),
			Parameters:  json.RawMessage(schemaJSON),
		})
	}
	return schemas
}

// convertResponse 将 provider.Response 转换为通用格式
func (a *Agent) convertResponse(resp provider.Response) map[string]interface{} {
	result := map[string]interface{}{
		"role": "assistant",
	}

	// 如果有工具调用
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
		// 纯文本响应
		result["content"] = resp.Content
	}

	return result
}

// getString 从 map 中安全获取字符串
func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}
