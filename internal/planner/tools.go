package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/registry"
)

// ObserveStateTool 观察探索图状态
type ObserveStateTool struct {
	registry.BaseTool
	graph *explorationgraph.Store
}

// NewObserveStateTool 构造探索图状态观察工具（planner 专属）。只读、并发安全。
func NewObserveStateTool(graph *explorationgraph.Store) *ObserveStateTool {
	t := &ObserveStateTool{graph: graph}
	t.WithTimeout(30 * time.Second).WithConcurrencySafe(true)
	return t
}

// Name 实现工具接口。
func (t *ObserveStateTool) Name() string { return "observe_state" }

// ShortDesc 实现工具接口。
func (t *ObserveStateTool) ShortDesc() string {
	return "观察探索图状态"
}

// Desc 实现工具接口。
func (t *ObserveStateTool) Desc() string {
	return "观察当前探索图状态（目标、Action、观察、发现）"
}

// Schema 实现工具接口。
func (t *ObserveStateTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"include_completed": {
				"type": "boolean",
				"description": "是否包含已完成的 Action（默认 false）"
			}
		}
	}`)
}

// Execute 实现工具接口：汇总探索图当前状态。
func (t *ObserveStateTool) Execute(ctx context.Context, argsJSON json.RawMessage) (registry.ToolResult, error) {
	taskID, ok := ctx.Value("task_id").(string)
	if !ok {
		return registry.ToolResult{Error: "task_id not in context"}, nil
	}

	var input struct {
		IncludeCompleted bool `json:"include_completed"`
	}
	if err := json.Unmarshal(argsJSON, &input); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("parse args: %v", err)}, nil
	}

	// 查询目标
	objectives, err := t.graph.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load objectives: %v", err)}, nil
	}

	// 查询 Action
	actions, err := t.graph.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load actions: %v", err)}, nil
	}

	// 过滤已完成的 Action
	if !input.IncludeCompleted {
		var filtered []explorationgraph.Node
		for _, action := range actions {
			if action.State != nil && *action.State != explorationgraph.StateDone {
				filtered = append(filtered, action)
			}
		}
		actions = filtered
	}

	// 查询发现
	findings, err := t.graph.ListNodesByKind(ctx, taskID, core.KindResult)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load findings: %v", err)}, nil
	}

	// 格式化输出
	var output string
	output += "## 探索图状态\n\n"

	// 目标
	output += fmt.Sprintf("### 目标 (%d)\n", len(objectives))
	for _, obj := range objectives {
		output += fmt.Sprintf("- %s\n", string(obj.Content))
	}
	output += "\n"

	// Action
	output += fmt.Sprintf("### Action (%d)\n", len(actions))
	stateCounts := make(map[explorationgraph.State]int)
	for _, action := range actions {
		if action.State != nil {
			stateCounts[*action.State]++
		}
	}
	for state, count := range stateCounts {
		output += fmt.Sprintf("- %s: %d\n", state, count)
	}
	output += "\n"

	// 发现
	output += fmt.Sprintf("### 发现 (%d)\n", len(findings))
	for _, finding := range findings {
		output += fmt.Sprintf("- %s\n", string(finding.Content))
	}

	return registry.ToolResult{Output: output}, nil
}

// EvaluateProgressTool 评估任务进展
type EvaluateProgressTool struct {
	registry.BaseTool

	graph *explorationgraph.Store
}

// NewEvaluateProgressTool 构造进展评估工具（planner 专属）。
func NewEvaluateProgressTool(graph *explorationgraph.Store) *EvaluateProgressTool {
	t := &EvaluateProgressTool{graph: graph}
	t.WithTimeout(30 * time.Second).WithConcurrencySafe(true)
	return t
}

// Name 实现工具接口。
func (t *EvaluateProgressTool) Name() string { return "evaluate_progress" }

// ShortDesc 实现 EvaluateProgressTool 的接口方法。
func (t *EvaluateProgressTool) ShortDesc() string {
	return "评估任务进展"
}

// Desc 实现 EvaluateProgressTool 的接口方法。

// Desc 实现工具接口。
func (t *EvaluateProgressTool) Desc() string {
	return "评估任务进展，判断是否应该继续生成 Action"
	// Schema 实现 EvaluateProgressTool 的接口方法。
}

// Schema 实现工具接口。
func (t *EvaluateProgressTool) Schema() json.RawMessage {
	// Execute 实现 EvaluateProgressTool 的接口方法。
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

// Execute 实现工具接口：汇总当前进展。
func (t *EvaluateProgressTool) Execute(ctx context.Context, _ json.RawMessage) (registry.ToolResult, error) {
	taskID, ok := ctx.Value("task_id").(string)
	if !ok {
		return registry.ToolResult{Error: "task_id not in context"}, nil
	}

	// 查询所有 Action
	actions, err := t.graph.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load actions: %v", err)}, nil
	}

	// 统计状态
	stateCounts := make(map[explorationgraph.State]int)
	for _, action := range actions {
		if action.State != nil {
			stateCounts[*action.State]++
		}
	}

	// 查询发现
	findings, err := t.graph.ListNodesByKind(ctx, taskID, core.KindResult)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load findings: %v", err)}, nil
	}

	// 评估进展
	totalActions := len(actions)
	doneActions := stateCounts[explorationgraph.StateDone]
	openActions := stateCounts[explorationgraph.StateOpen]
	runningActions := stateCounts[explorationgraph.StateRunning]

	completionRate := 0.0
	if totalActions > 0 {
		completionRate = float64(doneActions) / float64(totalActions) * 100
	}

	var output string
	output += "## 任务进展\n\n"
	output += fmt.Sprintf("- 总 Action 数: %d\n", totalActions)
	output += fmt.Sprintf("- 已完成: %d\n", doneActions)
	output += fmt.Sprintf("- 执行中: %d\n", runningActions)
	output += fmt.Sprintf("- 待执行: %d\n", openActions)
	output += fmt.Sprintf("- 完成率: %.1f%%\n\n", completionRate)
	output += fmt.Sprintf("- 发现数: %d\n\n", len(findings))

	// 判断
	switch {
	case openActions > 0 || runningActions > 0:
		output += "建议：还有待执行或执行中的 Action，暂时不需要生成新 Action。\n"
	case completionRate >= 80 && len(findings) > 0:
		output += "建议：任务进展良好，已有足够发现，可以考虑结束。\n"
	default:
		output += "建议：可以根据当前发现生成新 Action。\n"
	}

	return registry.ToolResult{Output: output}, nil
}
