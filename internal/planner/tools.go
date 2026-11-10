package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/registry"
)

// plannerCtxKey 是 planner 包内 context 值的类型化键（SA1029：禁止裸 string 键）。
type plannerCtxKey string

const ctxKeyTaskID plannerCtxKey = "task_id"

// WithTaskID 向 ctx 注入 task_id（observe_state/evaluate_progress 经它取归属）。
func WithTaskID(ctx context.Context, taskID string) context.Context {
	return context.WithValue(ctx, ctxKeyTaskID, taskID)
}

// taskIDFromContext 取注入的 task_id；未注入返回空串。
func taskIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyTaskID).(string)
	return v
}

// ObserveStateTool 观察探索图状态
type ObserveStateTool struct {
	registry.BaseTool
	graph *explorationgraph.Store
}

// NewObserveStateTool 构造探索图状态观察工具（planner 专属）。只读、并发安全。
func NewObserveStateTool(graph *explorationgraph.Store) *ObserveStateTool {
	t := &ObserveStateTool{graph: graph}
	t.WithTimeout(30 * time.Second)
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
	taskID := taskIDFromContext(ctx)
	if taskID == "" {
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
			if action.State != nil && *action.State != explorationgraph.StateCompleted {
				filtered = append(filtered, action)
			}
		}
		actions = filtered
	}

	// 查询发现（经裁决坐实的主张）
	findings, err := t.graph.ListNodesByKind(ctx, taskID, core.KindResult)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load findings: %v", err)}, nil
	}

	// 查询已确认的世界状态（fact 观察终态）
	observations, err := t.graph.ListNodesByKind(ctx, taskID, core.KindObservation)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load observations: %v", err)}, nil
	}
	var facts []explorationgraph.Node
	for _, obs := range observations {
		if obs.Confidence != nil && *obs.Confidence == explorationgraph.ConfidenceFactual {
			facts = append(facts, obs)
		}
	}

	// 格式化输出
	var output string
	output += "## 探索图状态\n\n"

	// 目标
	output += fmt.Sprintf("### 目标 (%d)\n", len(objectives))
	for _, obj := range objectives {
		state := "无状态"
		if obj.State != nil {
			state = string(*obj.State)
		}
		output += fmt.Sprintf("- [%s] %s\n", state, string(obj.Content))
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
	// retryable 中断动作逐条列出：这些是 runner 停摆导致的失败（非探索路径被堵），
	// 恢复重投后的新 planner 应优先重做——藏在计数里 planner 看不到个体。
	var retryables []string
	for _, action := range actions {
		if action.State == nil || *action.State != explorationgraph.StateFailed || len(action.Metadata) == 0 {
			continue
		}
		var meta map[string]any
		if json.Unmarshal(action.Metadata, &meta) == nil && meta["retryable"] == true {
			id := action.ID
			if len(id) > 8 {
				id = id[:8]
			}
			retryables = append(retryables, fmt.Sprintf("- %s %s", id, action.Content))
		}
	}
	if len(retryables) > 0 {
		output += fmt.Sprintf("\n**可重做的中断动作（%d，retryable——runner 停摆所致，优先重做而非换路）：**\n", len(retryables))
		for _, r := range retryables {
			output += r + "\n"
		}
	}
	output += "\n"

	// 已确认的世界状态（规划基底）：只渲染 statement（renderStatements——
	// 观察载荷里的 repro/execution_trace 是验证与审计材料，对规划是噪音）
	if len(facts) > 0 {
		output += fmt.Sprintf("### 已确认事实 (%d)\n", len(facts))
		output += renderStatements(facts)
		output += "\n"
	}

	// 发现：statement（priority 随行），其余载荷属交付
	output += fmt.Sprintf("### 发现 (%d)\n", len(findings))
	output += renderFindings(findings)

	return registry.ToolResult{Output: output}, nil
}

// renderStatements 渲染节点列表的 statement 行——planner 需要的是世界状态
// 陈述，不是观察的全部载荷（repro/trace 对规划是噪音，整包倾倒会撑爆
// observe_state 输出）。缺 statement 的载荷行跳过（解析失败不渲染噪音）。
func renderStatements(nodes []explorationgraph.Node) string {
	var b strings.Builder
	for _, n := range nodes {
		var c struct {
			Statement string `json:"statement"`
		}
		if json.Unmarshal(n.Content, &c) != nil || c.Statement == "" {
			continue
		}
		fmt.Fprintf(&b, "- %s\n", c.Statement)
	}
	return b.String()
}

// renderFindings 渲染 result 节点行：statement（priority 随行）——优先级是
// planner 排后续动作的输入，其余载荷（impact/suggestion/repro）属交付。
func renderFindings(nodes []explorationgraph.Node) string {
	var b strings.Builder
	for _, n := range nodes {
		var c struct {
			Statement string `json:"statement"`
			Priority  string `json:"priority"`
		}
		if json.Unmarshal(n.Content, &c) != nil || c.Statement == "" {
			continue
		}
		if c.Priority != "" {
			fmt.Fprintf(&b, "- [%s] %s\n", c.Priority, c.Statement)
		} else {
			fmt.Fprintf(&b, "- %s\n", c.Statement)
		}
	}
	return b.String()
}

// EvaluateProgressTool 评估任务进展
type EvaluateProgressTool struct {
	registry.BaseTool

	graph *explorationgraph.Store
}

// NewEvaluateProgressTool 构造进展评估工具（planner 专属）。
func NewEvaluateProgressTool(graph *explorationgraph.Store) *EvaluateProgressTool {
	t := &EvaluateProgressTool{graph: graph}
	t.WithTimeout(30 * time.Second)
	return t
}

// Name 实现工具接口。
func (t *EvaluateProgressTool) Name() string { return "evaluate_progress" }

// ShortDesc 实现工具接口。
func (t *EvaluateProgressTool) ShortDesc() string {
	return "评估任务进展"
}

// Desc 实现工具接口。
func (t *EvaluateProgressTool) Desc() string {
	return "评估任务进展，判断是否应该继续生成 Action"
}

// Schema 实现工具接口。
func (t *EvaluateProgressTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

// Execute 实现工具接口：汇总当前进展。
func (t *EvaluateProgressTool) Execute(ctx context.Context, _ json.RawMessage) (registry.ToolResult, error) {
	taskID := taskIDFromContext(ctx)
	if taskID == "" {
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
	doneActions := stateCounts[explorationgraph.StateCompleted]
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
