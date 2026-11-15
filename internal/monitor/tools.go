package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/monitor/metrics"
	"github.com/V3teran/liusha/internal/registry"
)

// ============================================
// GetGlobalStateTool - 获取任务全局状态
// ============================================

// GetGlobalStateTool 把探索图全局态势（objectives/actions/results 摘要）喂给 LLM。
type GetGlobalStateTool struct {
	registry.BaseTool
	graph            *explorationgraph.Store
	taskID           string
	metricsCollector *metrics.Collector
}

// NewGetGlobalStateTool 构造全局态势工具。
func NewGetGlobalStateTool(graph *explorationgraph.Store, taskID string, metricsCollector *metrics.Collector) *GetGlobalStateTool {
	t := &GetGlobalStateTool{
		graph:            graph,
		taskID:           taskID,
		metricsCollector: metricsCollector,
	}
	t.SetTimeout(constants.ToolTimeoutLong)
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
	return "获取任务的全局状态，包括 Objective、所有 Actions 和 Results"
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

	// 读取所有坐实发现（kind=result）
	findings, err := t.graph.ListResults(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("list results: %v", err)}, nil
	}

	// 根目标（任务本身）
	root, ok, err := t.graph.RootObjectiveOf(ctx, t.taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("get objective: %v", err)}, nil
	}
	if !ok {
		return registry.ToolResult{Error: "任务还没有根目标"}, nil
	}

	// 构建状态快照（在途动作的指令供判断重复与偏离；卡死由执行者看门狗处理）
	state := GlobalState{
		Objective:      ObjectiveView{ID: root.ID, Goal: root.Goal()},
		Actions:        allActions,
		Results:        findings,
		RunningActions: t.runningActionViews(allActions, time.Now()),
		Metrics:        t.metricsCollector.Snapshot(),
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
func (t *GetGlobalStateTool) runningActionViews(actions []explorationgraph.Node, now time.Time) []RunningActionView {
	var views []RunningActionView
	for _, a := range actions {
		if a.State == nil || *a.State != explorationgraph.StateRunning {
			continue
		}
		var c struct {
			Instruction string `json:"instruction"`
		}
		if err := json.Unmarshal(a.Content, &c); err != nil {
			continue // content 由 MustJSON 写入，解析失败=节点损坏，不进运行中视图
		}
		mins := now.Sub(a.UpdatedAt).Minutes()
		if mins < 0 {
			mins = 0
		}
		view := RunningActionView{
			ID:             a.ID,
			Instruction:    c.Instruction,
			RunningMinutes: math.Round(mins*10) / 10,
		}
		views = append(views, view)
	}
	return views
}

// ============================================
// PublishDecisionTool - 发布监察决策
// ============================================

// Replanner 接收监察者的重规划请求（规划者实现）。
type Replanner interface {
	RequestReplan(reason string)
}

// 监察决策类型：工具 schema 是决策名的唯一出处，章程只用文字描述何时干预。
const (
	// DecisionRejectAction 否决一个待执行或执行中的动作（落图 → rejected）。
	DecisionRejectAction = "reject_action"
	// DecisionRequestReplan 请求规划者带着理由重新规划（进程内直达，不落图）。
	DecisionRequestReplan = "request_replan"
)

// PublishDecisionTool 是 monitor 的决策出口：否决落探索图状态，重规划请求直接交给
// 规划者（控制信号不走可丢的事件总线，也不是图上的事实）。
type PublishDecisionTool struct {
	registry.BaseTool
	graph     *explorationgraph.Store
	replanner Replanner
	taskID    string
}

// NewPublishDecisionTool 构造决策发布工具。
func NewPublishDecisionTool(graph *explorationgraph.Store, replanner Replanner, taskID string) *PublishDecisionTool {
	t := &PublishDecisionTool{
		graph:     graph,
		replanner: replanner,
		taskID:    taskID,
	}
	t.SetTimeout(constants.ToolTimeoutMedium)
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
	return "发布监察决策：否决一个动作（reject_action），或请求规划者重新规划（request_replan）"
}

// Schema 实现工具接口。
func (t *PublishDecisionTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"type": {
				"type": "string",
				"description": "reject_action：否决一个待执行或执行中的动作（执行中的立即中断，规划者不再提出等效动作；用户指派的动作不可否决）。request_replan：请求规划者按 reason 重新规划。",
				"enum": ["reject_action", "request_replan"]
			},
			"action_id": {
				"type": "string",
				"description": "被否决动作的 ID（reject_action 必填）"
			},
			"reason": {
				"type": "string",
				"description": "决策理由：否决时写明为何这一步不该做（规划者据此避开等效方向），重规划时写明要修正什么"
			}
		},
		"required": ["type", "reason"]
	}`)
}

// Execute 实现工具接口：否决落探索图状态，重规划请求直达规划者。
func (t *PublishDecisionTool) Execute(ctx context.Context, args json.RawMessage) (registry.ToolResult, error) {
	var decision Decision
	if err := json.Unmarshal(args, &decision); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("invalid arguments: %v", err)}, nil
	}

	switch decision.Type {
	case DecisionRejectAction:
		if decision.ActionID == "" {
			return registry.ToolResult{Error: "action_id is required for reject_action"}, nil
		}
		if msg := t.rejectAction(ctx, decision); msg != "" {
			return registry.ToolResult{Error: msg}, nil
		}
	case DecisionRequestReplan:
		t.replanner.RequestReplan(decision.Reason)
	default:
		return registry.ToolResult{Error: "type must be reject_action or request_replan"}, nil
	}

	result := map[string]interface{}{
		"type":    decision.Type,
		"reason":  decision.Reason,
		"applied": true,
	}
	if decision.ActionID != "" {
		result["action_id"] = decision.ActionID
	}
	resultJSON, _ := json.Marshal(result)
	return registry.ToolResult{Output: string(resultJSON)}, nil
}

// rejectAction 落一次否决，返回给模型的拒绝说明（空 = 已生效）。用户指派的动作是人的
// 明确意图，监察者无权否决——只有用户自己能否决它。
func (t *PublishDecisionTool) rejectAction(ctx context.Context, decision Decision) string {
	node, err := t.graph.GetNode(ctx, decision.ActionID)
	if err != nil || node.TaskID != t.taskID || node.Kind != core.KindAction {
		return fmt.Sprintf("reject_action 拒绝：%s 不是本任务的动作", decision.ActionID)
	}
	if node.SourceType == explorationgraph.SourceUser {
		return fmt.Sprintf("reject_action 拒绝：动作 %s 是用户指派的，只有用户能否决", decision.ActionID)
	}
	ok, err := t.graph.RejectAction(ctx, t.taskID, decision.ActionID, explorationgraph.SourceMonitor, decision.Reason)
	if err != nil {
		return fmt.Sprintf("reject_action 失败: %v", err)
	}
	if !ok {
		return fmt.Sprintf("reject_action 拒绝：动作 %s 已结束，只有待执行或执行中的动作可否决", decision.ActionID)
	}
	return ""
}
