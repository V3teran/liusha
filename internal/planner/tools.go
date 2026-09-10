package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// ObserveStateTool 观察世界模型状态
type ObserveStateTool struct {
	world *knowledgegraph.Store
}

func NewObserveStateTool(world *knowledgegraph.Store) *ObserveStateTool {
	return &ObserveStateTool{world: world}
}

func (t *ObserveStateTool) Name() string { return "observe_state" }

func (t *ObserveStateTool) ShortDesc() string {
	return "观察世界模型状态"
}

func (t *ObserveStateTool) Desc() string {
	return "观察当前世界模型状态（目标、Action、观察、发现）"
}

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
	objectives, err := t.world.ListNodesByKind(ctx, taskID, knowledgegraph.KindObjective)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load objectives: %v", err)}, nil
	}

	// 查询 Action
	actions, err := t.world.ListNodesByKind(ctx, taskID, knowledgegraph.KindAction)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load actions: %v", err)}, nil
	}

	// 过滤已完成的 Action
	if !input.IncludeCompleted {
		var filtered []knowledgegraph.Node
		for _, action := range actions {
			if action.State != nil && *action.State != knowledgegraph.StateDone {
				filtered = append(filtered, action)
			}
		}
		actions = filtered
	}

	// 查询发现
	findings, err := t.world.ListNodesByKind(ctx, taskID, knowledgegraph.KindResult)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load findings: %v", err)}, nil
	}

	// 格式化输出
	var output string
	output += fmt.Sprintf("## 世界模型状态\n\n")

	// 目标
	output += fmt.Sprintf("### 目标 (%d)\n", len(objectives))
	for _, obj := range objectives {
		output += fmt.Sprintf("- %s\n", string(obj.Content))
	}
	output += "\n"

	// Action
	output += fmt.Sprintf("### Action (%d)\n", len(actions))
	stateCounts := make(map[knowledgegraph.State]int)
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

// ProposeActionsTool 生成新的 Action
type ProposeActionsTool struct {
	world  *knowledgegraph.Store
	logger zerolog.Logger
}

func NewProposeActionsTool(world *knowledgegraph.Store, logger zerolog.Logger) *ProposeActionsTool {
	return &ProposeActionsTool{
		world:  world,
		logger: logger.With().Str("tool", "propose_actions").Logger(),
	}
}

func (t *ProposeActionsTool) Name() string { return "propose_actions" }

func (t *ProposeActionsTool) ShortDesc() string {
	return "生成新的 Action"
}

func (t *ProposeActionsTool) Desc() string {
	return "生成新的 Action（执行动作）"
}

func (t *ProposeActionsTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"actions": {
				"type": "array",
				"description": "Action 列表",
				"items": {
					"type": "object",
					"properties": {
						"instruction": {
							"type": "string",
							"description": "自然语言描述要做什么（必填）"
						},
						"complexity": {
							"type": "string",
							"enum": ["trivial", "simple", "moderate", "complex", "extreme"],
							"description": "执行复杂度（必填）"
						},
						"priority": {
							"type": "string",
							"description": "优先级（必填）：critical=P0, high=P1, medium=P2, low=P3",
								"enum": ["critical", "high", "medium", "low"],
						},
						"depends_on": {
							"type": "array",
							"description": "依赖的其他 Action ID 列表（可选）",
							"items": {"type": "string"}
						},
						"roadmap_step": {
							"type": "number",
							"description": "关联的 RoadmapStep 编号（可选）"
						}
					},
					"required": ["instruction", "complexity", "priority"]
				}
			}
		},
		"required": ["actions"]
	}`)
}

func (t *ProposeActionsTool) Execute(ctx context.Context, argsJSON json.RawMessage) (registry.ToolResult, error) {
	taskID, ok := ctx.Value("task_id").(string)
	if !ok {
		return registry.ToolResult{Error: "task_id not in context"}, nil
	}

	var input struct {
		Actions []struct {
			Instruction  string    `json:"instruction"`
			Complexity   string    `json:"complexity"`
			Priority     string    `json:"priority"` // critical/high/medium/low
			DependsOn    []string  `json:"depends_on"`
			RoadmapStep  *float64  `json:"roadmap_step"`
		} `json:"actions"`
	}

	if err := json.Unmarshal(argsJSON, &input); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("parse args: %v", err)}, nil
	}

	if len(input.Actions) == 0 {
		return registry.ToolResult{Error: "actions cannot be empty"}, nil
	}

	var createdIDs []string

	for _, a := range input.Actions {
		if a.Instruction == "" {
			return registry.ToolResult{Error: "instruction must be non-empty"}, nil
		}

		complexity := knowledgegraph.Complexity(a.Complexity)
		priority := knowledgegraph.Priority(a.Priority)
		state := knowledgegraph.StateOpen

		content, _ := json.Marshal(map[string]interface{}{
			"instruction": a.Instruction,
		})

		node := knowledgegraph.Node{
			ID:          uuid.New().String(),
			TaskID:      taskID,
			Kind:        knowledgegraph.KindAction,
			Content:     content,
			State:       &state,
			Complexity:  &complexity,
			DependsOn:   a.DependsOn,
			RoadmapStep: a.RoadmapStep,
			Priority:    priority,
			Owner:       "planner",
			SourceType:  knowledgegraph.SourcePlanner,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		id, err := t.world.CreateNode(ctx, node)
		if err != nil {
			t.logger.Error().Err(err).Msg("failed to create action")
			return registry.ToolResult{Error: fmt.Sprintf("create action: %v", err)}, nil
		}

		createdIDs = append(createdIDs, id)
	}

	t.logger.Info().
		Str("task_id", taskID).
		Int("actions_count", len(createdIDs)).
		Msg("actions created")

	output := fmt.Sprintf("✓ 已创建 %d 个 Action\nIDs: %v", len(createdIDs), createdIDs)
	return registry.ToolResult{Output: output}, nil
}

// EvaluateProgressTool 评估任务进展
type EvaluateProgressTool struct {
	world *knowledgegraph.Store
}

func NewEvaluateProgressTool(world *knowledgegraph.Store) *EvaluateProgressTool {
	return &EvaluateProgressTool{world: world}
}

func (t *EvaluateProgressTool) Name() string { return "evaluate_progress" }

func (t *EvaluateProgressTool) ShortDesc() string {
	return "评估任务进展"
}

func (t *EvaluateProgressTool) Desc() string {
	return "评估任务进展，判断是否应该继续生成 Action"
}

func (t *EvaluateProgressTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *EvaluateProgressTool) Execute(ctx context.Context, argsJSON json.RawMessage) (registry.ToolResult, error) {
	taskID, ok := ctx.Value("task_id").(string)
	if !ok {
		return registry.ToolResult{Error: "task_id not in context"}, nil
	}

	// 查询所有 Action
	actions, err := t.world.ListNodesByKind(ctx, taskID, knowledgegraph.KindAction)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load actions: %v", err)}, nil
	}

	// 统计状态
	stateCounts := make(map[knowledgegraph.State]int)
	for _, action := range actions {
		if action.State != nil {
			stateCounts[*action.State]++
		}
	}

	// 查询发现
	findings, err := t.world.ListNodesByKind(ctx, taskID, knowledgegraph.KindResult)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load findings: %v", err)}, nil
	}

	// 评估进展
	totalActions := len(actions)
	doneActions := stateCounts[knowledgegraph.StateDone]
	openActions := stateCounts[knowledgegraph.StateOpen]
	runningActions := stateCounts[knowledgegraph.StateRunning]

	completionRate := 0.0
	if totalActions > 0 {
		completionRate = float64(doneActions) / float64(totalActions) * 100
	}

	var output string
	output += fmt.Sprintf("## 任务进展\n\n")
	output += fmt.Sprintf("- 总 Action 数: %d\n", totalActions)
	output += fmt.Sprintf("- 已完成: %d\n", doneActions)
	output += fmt.Sprintf("- 执行中: %d\n", runningActions)
	output += fmt.Sprintf("- 待执行: %d\n", openActions)
	output += fmt.Sprintf("- 完成率: %.1f%%\n\n", completionRate)
	output += fmt.Sprintf("- 发现数: %d\n\n", len(findings))

	// 判断
	if openActions > 0 || runningActions > 0 {
		output += "建议：还有待执行或执行中的 Action，暂时不需要生成新 Action。\n"
	} else if completionRate >= 80 && len(findings) > 0 {
		output += "建议：任务进展良好，已有足够发现，可以考虑结束。\n"
	} else {
		output += "建议：可以根据当前发现生成新 Action。\n"
	}

	return registry.ToolResult{Output: output}, nil
}
