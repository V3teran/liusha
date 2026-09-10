package planner

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// GenerateRoadmapTool 生成或更新 Roadmap
type GenerateRoadmapTool struct {
	world  *knowledgegraph.Store
	logger zerolog.Logger
}

func NewGenerateRoadmapTool(world *knowledgegraph.Store, logger zerolog.Logger) *GenerateRoadmapTool {
	return &GenerateRoadmapTool{
		world:  world,
		logger: logger.With().Str("tool", "generate_roadmap").Logger(),
	}
}

func (t *GenerateRoadmapTool) Name() string { return "generate_roadmap" }

func (t *GenerateRoadmapTool) ShortDesc() string {
	return "生成或更新 Roadmap"
}

func (t *GenerateRoadmapTool) Desc() string {
	return "生成或更新任务的 Roadmap（探索式规划路线图）"
}

func (t *GenerateRoadmapTool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"steps": {
				"type": "array",
				"description": "完整的 Roadmap 步骤列表（10-15 个步骤，中粒度）",
				"items": {
					"type": "object",
					"properties": {
						"step": {
							"type": "number",
							"description": "步骤编号（如 1.0, 2.0, 支持小数如 1.5）"
						},
						"objective": {
							"type": "string",
							"description": "步骤目标（自然语言）"
						},
						"depends_on": {
							"type": "array",
							"description": "依赖的步骤编号",
							"items": {"type": "number"}
						},
						"rationale": {
							"type": "string",
							"description": "为什么规划这一步（可选）"
						}
					},
					"required": ["step", "objective"]
				}
			}
		},
		"required": ["steps"]
	}`)
}

func (t *GenerateRoadmapTool) Execute(ctx context.Context, argsJSON json.RawMessage) (registry.ToolResult, error) {
	taskID, ok := ctx.Value("task_id").(string)
	if !ok {
		return registry.ToolResult{Error: "task_id not in context"}, nil
	}

	var input struct {
		Steps []struct {
			Step      float64   `json:"step"`
			Objective string    `json:"objective"`
			DependsOn []float64 `json:"depends_on"`
			Rationale string    `json:"rationale"`
		} `json:"steps"`
	}

	if err := json.Unmarshal(argsJSON, &input); err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("parse args: %v", err)}, nil
	}

	if len(input.Steps) == 0 {
		return registry.ToolResult{Error: "steps cannot be empty"}, nil
	}

	var steps []knowledgegraph.RoadmapStep
	for _, s := range input.Steps {
		if s.Objective == "" {
			return registry.ToolResult{Error: "step.objective must be non-empty"}, nil
		}

		step := knowledgegraph.RoadmapStep{
			TaskID:    taskID,
			Step:      s.Step,
			Objective: s.Objective,
			Status:    knowledgegraph.StepTodo,
			DependsOn: s.DependsOn,
			Context:   make(map[string]interface{}),
			Rationale: s.Rationale,
		}
		steps = append(steps, step)
	}

	knowledgegraph.SortStepsByNumber(steps)

	err := t.world.SaveRoadmap(ctx, taskID, steps)
	if err != nil {
		t.logger.Error().Err(err).Str("task_id", taskID).Msg("failed to save roadmap")
		return registry.ToolResult{Error: fmt.Sprintf("save roadmap: %v", err)}, nil
	}

	t.logger.Info().Str("task_id", taskID).Int("steps_count", len(steps)).Msg("roadmap saved")

	summary, _ := t.world.GetRoadmapSummary(ctx, taskID)

	output := fmt.Sprintf("✓ Roadmap 已生成，共 %d 个步骤\n待执行: %d, 执行中: %d, 已完成: %d",
		summary.TotalSteps, summary.TodoSteps, summary.ActiveSteps, summary.CompleteSteps)

	return registry.ToolResult{Output: output}, nil
}

// ObserveRoadmapTool 观察当前的 Roadmap
type ObserveRoadmapTool struct {
	world  *knowledgegraph.Store
	logger zerolog.Logger
}

func NewObserveRoadmapTool(world *knowledgegraph.Store, logger zerolog.Logger) *ObserveRoadmapTool {
	return &ObserveRoadmapTool{
		world:  world,
		logger: logger.With().Str("tool", "observe_roadmap").Logger(),
	}
}

func (t *ObserveRoadmapTool) Name() string { return "observe_roadmap" }

func (t *ObserveRoadmapTool) ShortDesc() string {
	return "观察当前 Roadmap 状态"
}

func (t *ObserveRoadmapTool) Desc() string {
	return "观察当前任务的 Roadmap 状态"
}

func (t *ObserveRoadmapTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {}}`)
}

func (t *ObserveRoadmapTool) Execute(ctx context.Context, argsJSON json.RawMessage) (registry.ToolResult, error) {
	taskID, ok := ctx.Value("task_id").(string)
	if !ok {
		return registry.ToolResult{Error: "task_id not in context"}, nil
	}

	steps, err := t.world.LoadRoadmap(ctx, taskID)
	if err != nil {
		return registry.ToolResult{Error: fmt.Sprintf("load roadmap: %v", err)}, nil
	}

	if len(steps) == 0 {
		return registry.ToolResult{Output: "当前任务还没有 Roadmap，请先生成"}, nil
	}

	summary, _ := t.world.GetRoadmapSummary(ctx, taskID)

	var output string
	output += fmt.Sprintf("## Roadmap 状态\n\n")
	output += fmt.Sprintf("- 总步骤: %d\n", summary.TotalSteps)
	output += fmt.Sprintf("- 待执行: %d\n", summary.TodoSteps)
	output += fmt.Sprintf("- 执行中: %d\n", summary.ActiveSteps)
	output += fmt.Sprintf("- 已完成: %d\n", summary.CompleteSteps)
	output += fmt.Sprintf("- 已跳过: %d\n", summary.SkippedSteps)
	output += fmt.Sprintf("- 完成率: %.1f%%\n\n", summary.CompletionRate*100)

	output += "## 步骤详情\n\n"
	for _, step := range steps {
		statusIcon := map[knowledgegraph.RoadmapStepStatus]string{
			knowledgegraph.StepTodo:     "⏸",
			knowledgegraph.StepActive:   "⏳",
			knowledgegraph.StepComplete: "✓",
			knowledgegraph.StepSkipped:  "⊗",
		}[step.Status]

		output += fmt.Sprintf("%s %.1f: %s (%s)\n", statusIcon, step.Step, step.Objective, step.Status)

		if len(step.DependsOn) > 0 {
			output += fmt.Sprintf("  依赖: %v\n", step.DependsOn)
		}

		if step.Rationale != "" {
			output += fmt.Sprintf("  理由: %s\n", step.Rationale)
		}
	}

	return registry.ToolResult{Output: output}, nil
}
