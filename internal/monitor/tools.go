package monitor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/registry"
)

// ============================================
// GetGlobalStateTool - 获取任务全局状态
// ============================================

// GetGlobalStateTool 把探索图全局态势（objectives/actions/results 摘要）喂给 LLM。
type GetGlobalStateTool struct {
	registry.BaseTool
	world  *explorationgraph.Store
	taskID string
}

// NewGetGlobalStateTool 构造全局态势工具。
func NewGetGlobalStateTool(world *explorationgraph.Store, taskID string) *GetGlobalStateTool {
	t := &GetGlobalStateTool{
		world:  world,
		taskID: taskID,
	}
	t.SetTimeout(constants.ToolTimeoutLong)
	t.SetConcurrencySafe(true)
	return t
}

// Name 实现工具接口。
func (t *GetGlobalStateTool) Name() string {
	return "get_global_state"
}

// ShortDesc 实现工具接口。
func (t *GetGlobalStateTool) ShortDesc() string {
	return "获取任务全局状态"
}

// Desc 实现工具接口。
func (t *GetGlobalStateTool) Desc() string {
	return "获取任务的全局状态，包括 Objective、所有 Actions 和 Findings"
}

// Schema 实现工具接口。
func (t *GetGlobalStateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {},
		"required": []
	}`)
}

// Execute 实现工具接口：读探索图汇总全局态势。
func (t *GetGlobalStateTool) Execute(ctx context.Context, _ json.RawMessage) (registry.ToolResult, error) {
	// 读取所有 Actions
	allActions, err := t.world.ListAllActions(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("list actions: %v", err)}, nil
	}

	// 读取所有 Findings
	findings, err := t.world.ListResults(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("list findings: %v", err)}, nil
	}

	// 读取 Objective
	objective, err := t.world.GetObjective(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("get objective: %v", err)}, nil
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
		return registry.ToolResult{Error: fmt.Sprintf("marshal state: %v", err)}, nil
	}

	return registry.ToolResult{Output: string(stateJSON)}, nil
}

// ============================================
// PublishDecisionTool - 发布监察决策
// ============================================

// PublishDecisionTool 是 monitor 的决策出口（kill_action 落探索图状态变更）。
type PublishDecisionTool struct {
	registry.BaseTool
	world  *explorationgraph.Store
	taskID string
}

// NewPublishDecisionTool 构造决策发布工具。kill_action 的生效路径是探索图
// 状态变更：action 置 aborted 后 executor 不再认领（CanExecute 只认 open）；
// request_replan 无需显式事件——planner 以 10s 轮询兜底重规划。
func NewPublishDecisionTool(world *explorationgraph.Store, taskID string) *PublishDecisionTool {
	t := &PublishDecisionTool{
		world:  world,
		taskID: taskID,
	}
	t.SetTimeout(constants.ToolTimeoutMedium)
	t.SetConcurrencySafe(false) // 决策操作不能并发
	return t
}

// Name 实现工具接口。
func (t *PublishDecisionTool) Name() string {
	return "publish_decision"
}

// ShortDesc 实现工具接口。
func (t *PublishDecisionTool) ShortDesc() string {
	return "发布监察决策"
}

// Desc 实现工具接口。
func (t *PublishDecisionTool) Desc() string {
	return "发布监察决策事件（kill_action 或 request_replan）"
}

// Schema 实现工具接口。
func (t *PublishDecisionTool) Schema() json.RawMessage {
	return json.RawMessage(`{
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
}

// Execute 实现工具接口：kill_action 落探索图状态变更。
// Execute 实现工具接口：kill_action 落探索图状态变更。
func (t *PublishDecisionTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	// 解析参数
	var decision Decision
	if err := json.Unmarshal(args, &decision); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("invalid arguments: %v", err)}, nil
	}

	// 验证参数
	if decision.Type != "kill_action" && decision.Type != "request_replan" {
		return registry.ToolResult{Error: "type must be kill_action or request_replan"}, nil
	}

	if decision.Type == "kill_action" && decision.ActionID == "" {
		return registry.ToolResult{Error: "action_id is required for kill_action"}, nil
	}

	// kill_action 直接落探索图状态：aborted 后 executor 不再认领（CanExecute 只认 open）。
	// request_replan 由 planner 的 10s 轮询兜底重规划吸收，无需显式事件。
	if decision.Type == "kill_action" && t.world != nil {
		reason := decision.Reason
		if err := t.world.UpdateActionStateWithReason(ctx, decision.ActionID, explorationgraph.StateAborted, &reason); err != nil {
			return registry.ToolResult{Error: fmt.Sprintf("kill_action 状态变更失败: %v", err)}, nil
		}
	}

	result := map[string]interface{}{
		"type":    decision.Type,
		"reason":  decision.Reason,
		"applied": decision.Type == "kill_action",
	}
	if decision.ActionID != "" {
		result["action_id"] = decision.ActionID
	}
	resultJSON, _ := json.Marshal(result)
	return registry.ToolResult{Output: string(resultJSON)}, nil
}
