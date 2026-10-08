package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/registry"
)

// ============================================
// GetGlobalStateTool - 获取任务全局状态
// ============================================

// GetGlobalStateTool 把探索图全局态势（objectives/actions/results 摘要）喂给 LLM。
type GetGlobalStateTool struct {
	registry.BaseTool
	graph  *explorationgraph.Store
	taskID string
}

// NewGetGlobalStateTool 构造全局态势工具。
func NewGetGlobalStateTool(graph *explorationgraph.Store, taskID string) *GetGlobalStateTool {
	t := &GetGlobalStateTool{
		graph:  graph,
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
	allActions, err := t.graph.ListAllActions(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("list actions: %v", err)}, nil
	}

	// 读取所有 Findings
	findings, err := t.graph.ListResults(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("list findings: %v", err)}, nil
	}

	// 读取 Objective
	objective, err := t.graph.GetObjective(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("get objective: %v", err)}, nil
	}

	// 构建状态快照（running 动作的时长视图是 monitor kill 判断的决策变量）
	state := GlobalState{
		Objective:      objective,
		Actions:        allActions,
		Findings:       findings,
		RunningActions: runningActionViews(allActions, time.Now()),
	}

	// 序列化为 JSON
	stateJSON, err := json.Marshal(state)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("marshal state: %v", err)}, nil
	}

	return registry.ToolResult{Output: string(stateJSON)}, nil
}

// runningActionViews 从动作列表抽取 running 态的监察视图。
// 时长基准取 UpdatedAt（最近一次状态迁移时刻：open→running 时刷新）。
func runningActionViews(actions []explorationgraph.Node, now time.Time) []RunningActionView {
	var views []RunningActionView
	for _, a := range actions {
		if a.State == nil || *a.State != explorationgraph.StateRunning {
			continue
		}
		var c struct {
			Instruction string `json:"instruction"`
		}
		_ = json.Unmarshal(a.Content, &c)
		mins := now.Sub(a.UpdatedAt).Minutes()
		if mins < 0 {
			mins = 0
		}
		views = append(views, RunningActionView{
			ID:             a.ID,
			Instruction:    c.Instruction,
			RunningMinutes: math.Round(mins*10) / 10,
		})
	}
	return views
}

// ============================================
// PublishDecisionTool - 发布监察决策
// ============================================

// PublishDecisionTool 是 monitor 的决策出口（kill_action 落探索图状态变更）。
type PublishDecisionTool struct {
	registry.BaseTool
	graph    *explorationgraph.Store
	eventBus bus.Bus // ✅ P2：需要 eventBus 发布 replan 事件
	taskID   string
}

// NewPublishDecisionTool 构造决策发布工具。
// ✅ P2：添加 eventBus 参数以支持 request_replan 事件发布
func NewPublishDecisionTool(graph *explorationgraph.Store, eventBus bus.Bus, taskID string) *PublishDecisionTool {
	t := &PublishDecisionTool{
		graph:    graph,
		eventBus: eventBus,
		taskID:   taskID,
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
	// ✅ P2：request_replan 发布事件 + 写入图的 metadata
	if decision.Type == "kill_action" && t.graph != nil {
		reason := decision.Reason
		if err := t.graph.UpdateActionStateWithReason(ctx, decision.ActionID, explorationgraph.StateAborted, &reason); err != nil {
			return registry.ToolResult{Error: fmt.Sprintf("kill_action 状态变更失败: %v", err)}, nil
		}
	}

	// ✅ P2：request_replan 落地
	if decision.Type == "request_replan" {
		// 1. 写入图的 metadata（供审计和后续查询）
		if err := t.writeReplanRequest(ctx, decision.Reason); err != nil {
			return registry.ToolResult{Error: fmt.Sprintf("write replan request: %v", err)}, nil
		}

		// 2. 发布事件（强制 Planner 立即重规划）
		t.eventBus.PublishReplanRequested(t.taskID, decision.Reason)
	}

	result := map[string]interface{}{
		"type":    decision.Type,
		"reason":  decision.Reason,
		"applied": decision.Type == "kill_action" || decision.Type == "request_replan",
	}
	if decision.ActionID != "" {
		result["action_id"] = decision.ActionID
	}
	resultJSON, _ := json.Marshal(result)
	return registry.ToolResult{Output: string(resultJSON)}, nil
}

// writeReplanRequest 将 monitor 的重规划请求写入 objective 的 metadata
func (t *PublishDecisionTool) writeReplanRequest(ctx context.Context, reason string) error {
	// 获取 objective
	objectives, err := t.graph.ListNodesByKind(ctx, t.taskID, core.KindObjective)
	if err != nil {
		return fmt.Errorf("list objectives: %w", err)
	}
	if len(objectives) == 0 {
		return fmt.Errorf("no objective found for task %s", t.taskID)
	}

	// 构造 metadata
	metadata := map[string]interface{}{
		"monitor_replan_request": map[string]interface{}{
			"reason":       reason,
			"requested_at": time.Now().Format(time.RFC3339),
		},
	}

	// 合并到现有 content
	var content map[string]interface{}
	if err := json.Unmarshal(objectives[0].Content, &content); err != nil {
		// 如果解析失败，创建新的
		content = make(map[string]interface{})
	}
	content["monitor_request"] = metadata

	newContent, err := json.Marshal(content)
	if err != nil {
		return fmt.Errorf("marshal content: %w", err)
	}

	// 更新图
	return t.graph.UpdateNodeContent(ctx, objectives[0].ID, newContent)
}
