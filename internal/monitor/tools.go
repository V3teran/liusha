package monitor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// ============================================
// GetGlobalStateTool - 获取任务全局状态
// ============================================

type GetGlobalStateTool struct {
	world  *explorationgraph.Store
	taskID string
}

func NewGetGlobalStateTool(world *explorationgraph.Store, taskID string) *GetGlobalStateTool {
	return &GetGlobalStateTool{
		world:  world,
		taskID: taskID,
	}
}

func (t *GetGlobalStateTool) Name() string {
	return "get_global_state"
}

func (t *GetGlobalStateTool) Description() string {
	return "获取任务的全局状态，包括 Objective、所有 Actions 和 Findings"
}

func (t *GetGlobalStateTool) Schema() core.ToolSchema {
	inputSchema := json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)

	return core.ToolSchema{
		InputSchema: inputSchema,
	}
}

func (t *GetGlobalStateTool) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	// 读取所有 Actions
	allActions, err := t.world.ListAllActions(ctx, t.taskID)
	if err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("list actions: %v", err)}, nil
	}

	// 读取所有 Findings
	findings, err := t.world.ListResults(ctx, t.taskID)
	if err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("list findings: %v", err)}, nil
	}

	// 读取 Objective
	objective, err := t.world.GetObjective(ctx, t.taskID)
	if err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("get objective: %v", err)}, nil
	}

	// 构建状态快照
	state := GlobalState{
		Objective: objective,
		Actions:   allActions,
		Findings:  findings,
	}

	// 序列化为 JSON
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("marshal state: %v", err)}, nil
	}

	return core.ToolOutput{Result: stateJSON}, nil
}

// ============================================
// PublishDecisionTool - 发布监察决策
// ============================================

type PublishDecisionTool struct {
	eventBus bus.Bus
	taskID   string
}

func NewPublishDecisionTool(eventBus bus.Bus, taskID string) *PublishDecisionTool {
	return &PublishDecisionTool{
		eventBus: eventBus,
		taskID:   taskID,
	}
}

func (t *PublishDecisionTool) Name() string {
	return "publish_decision"
}

func (t *PublishDecisionTool) Description() string {
	return "发布监察决策事件（kill_action 或 request_replan）"
}

func (t *PublishDecisionTool) Schema() core.ToolSchema {
	inputSchema := json.RawMessage(`{
		"type": "object",
		"properties": {
			"type": {
				"type": "string",
				"description": "决策类型：kill_action 或 request_replan",
				"enum": ["kill_action", "request_replan"]
			},
			"action_id": {
				"type": "string",
				"description": "Action ID（kill_action 时必需）"
			},
			"reason": {
				"type": "string",
				"description": "决策理由"
			}
		},
		"required": ["type", "reason"]
	}`)

	return core.ToolSchema{
		InputSchema: inputSchema,
	}
}

func (t *PublishDecisionTool) Execute(ctx context.Context, input core.ToolInput) (core.ToolOutput, error) {
	// 解析参数
	var decision Decision
	if err := json.Unmarshal(input.Arguments, &decision); err != nil {
		return core.ToolOutput{Error: fmt.Sprintf("invalid arguments: %v", err)}, nil
	}

	// 验证参数
	if decision.Type != "kill_action" && decision.Type != "request_replan" {
		return core.ToolOutput{Error: "type must be kill_action or request_replan"}, nil
	}

	if decision.Type == "kill_action" && decision.ActionID == "" {
		return core.ToolOutput{Error: "action_id is required for kill_action"}, nil
	}

	// kill_action 的实际生效路径是世界模型中 action 状态变更（planner.Kill）；
	// request_replan 决策记录在 monitor 的决策日志中。

	result := map[string]interface{}{
		"type":   decision.Type,
		"reason": decision.Reason,
	}
	if decision.ActionID != "" {
		result["action_id"] = decision.ActionID
	}

	resultJSON, _ := json.Marshal(result)
	return core.ToolOutput{Result: resultJSON}, nil
}
