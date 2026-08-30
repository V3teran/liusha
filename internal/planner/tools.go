package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// Tool 是 Planner Agent 使用的工具接口
type Tool interface {
	Name() string
	Description() string
	Schema() provider.ToolSchema
	Execute(ctx context.Context, input map[string]interface{}) (interface{}, error)
}

// ToolRegistry 管理工具注册
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry 创建工具注册表
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// Register 注册工具
func (r *ToolRegistry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

// Get 获取工具
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

// Schemas 返回所有工具的 schema
func (r *ToolRegistry) Schemas() []provider.ToolSchema {
	schemas := make([]provider.ToolSchema, 0, len(r.tools))
	for _, tool := range r.tools {
		schemas = append(schemas, tool.Schema())
	}
	return schemas
}

// ─────────────────────────────────────────────
//  observe_state 工具
// ─────────────────────────────────────────────

// ObserveStateTool 观察世界模型状态
type ObserveStateTool struct {
	world *worldmodel.Store
}

// NewObserveStateTool 创建工具
func NewObserveStateTool(world *worldmodel.Store) *ObserveStateTool {
	return &ObserveStateTool{world: world}
}

func (t *ObserveStateTool) Name() string { return "observe_state" }

func (t *ObserveStateTool) Description() string {
	return "观察当前世界模型状态（目标、Move、观察、发现）"
}

func (t *ObserveStateTool) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "observe_state",
		Description: "观察当前世界模型状态",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"include_completed_moves": {
					"type": "boolean",
					"description": "是否包含已完成的 Move（默认 false）"
				}
			}
		}`),
	}
}

func (t *ObserveStateTool) Execute(ctx context.Context, input map[string]interface{}) (interface{}, error) {
	// 从 context 获取 task_id（假设已注入）
	taskID, ok := ctx.Value("task_id").(string)
	if !ok {
		return nil, fmt.Errorf("task_id not in context")
	}

	includeCompleted := false
	if v, ok := input["include_completed_moves"].(bool); ok {
		includeCompleted = v
	}

	result := make(map[string]interface{})

	// 获取目标
	objectives, err := t.world.ListNodesByKind(ctx, taskID, worldmodel.KindObjective)
	if err != nil {
		return nil, fmt.Errorf("list objectives: %w", err)
	}
	result["objectives"] = objectives

	// 获取 open Move
	openMoves, err := t.world.ListOpenActions(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list open moves: %w", err)
	}
	result["open_moves"] = openMoves
	result["open_moves_count"] = len(openMoves)

	// 可选：已完成的 Move
	if includeCompleted {
		completedMoves, err := t.world.ListCompletedActions(ctx, taskID)
		if err != nil {
			return nil, fmt.Errorf("list completed moves: %w", err)
		}
		result["completed_moves"] = completedMoves
		result["completed_moves_count"] = len(completedMoves)
	}

	// 获取观察记录
	observations, err := t.world.ListNodesByKind(ctx, taskID, worldmodel.KindHypothesis)
	if err != nil {
		return nil, fmt.Errorf("list observations: %w", err)
	}
	result["observations_count"] = len(observations)

	// 获取重要发现
	discoveries, err := t.world.ListFindings(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list discoveries: %w", err)
	}
	result["discoveries"] = discoveries
	result["discoveries_count"] = len(discoveries)

	// 获取已验证的发现
	verifiedDiscoveries, err := t.world.ListVerifiedFindings(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list verified discoveries: %w", err)
	}
	result["verified_discoveries_count"] = len(verifiedDiscoveries)

	return result, nil
}

// ─────────────────────────────────────────────
//  propose_moves 工具
// ─────────────────────────────────────────────

// ProposeMovesTool 生成新的 Move
type ProposeMovesTool struct {
	world *worldmodel.Store
}

// NewProposeMovesTool 创建工具
func NewProposeMovesTool(world *worldmodel.Store) *ProposeMovesTool {
	return &ProposeMovesTool{world: world}
}

func (t *ProposeMovesTool) Name() string { return "propose_moves" }

func (t *ProposeMovesTool) Description() string {
	return "生成新的 Move（执行计划）"
}

func (t *ProposeMovesTool) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "propose_moves",
		Description: "生成新的 Move",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"moves": {
					"type": "array",
					"description": "要生成的 Move 列表",
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
							"target_ref": {
								"type": "object",
								"description": "目标定位（可选）",
								"properties": {
									"domain": {"type": "string"},
									"ref_kind": {"type": "string"},
									"locator": {"type": "string"}
								}
							},
							"priority": {
								"type": "integer",
								"description": "优先级 1-10（必填）",
								"minimum": 1,
								"maximum": 10
							},
							"depends_on": {
								"type": "array",
								"description": "依赖的其他 Move ID 列表（可选）",
								"items": {"type": "string"}
							}
						},
						"required": ["instruction", "complexity", "priority"]
					}
				}
			},
			"required": ["moves"]
		}`),
	}
}

func (t *ProposeMovesTool) Execute(ctx context.Context, input map[string]interface{}) (interface{}, error) {
	taskID, ok := ctx.Value("task_id").(string)
	if !ok {
		return nil, fmt.Errorf("task_id not in context")
	}

	movesInput, ok := input["moves"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("moves field missing or invalid")
	}

	created := make([]string, 0, len(movesInput))

	for _, m := range movesInput {
		moveMap, ok := m.(map[string]interface{})
		if !ok {
			continue
		}

		// 解析 Move 字段
		instruction, _ := moveMap["instruction"].(string)
		complexityStr, _ := moveMap["complexity"].(string)
		priority, _ := moveMap["priority"].(float64)

		if instruction == "" || complexityStr == "" {
			continue
		}

		// 解析 complexity
		complexity := worldmodel.Complexity(complexityStr)

		// 解析 target_ref（可选）
		var targetRef *worldmodel.TargetRef
		if tr, ok := moveMap["target_ref"].(map[string]interface{}); ok {
			domain, _ := tr["domain"].(string)
			refKind, _ := tr["ref_kind"].(string)
			locator, _ := tr["locator"].(string)
			targetRef = &worldmodel.TargetRef{
				Domain:  domain,
				RefKind: refKind,
				Locator: locator,
			}
		}

		// 解析 depends_on（可选）
		var dependsOn []string
		if deps, ok := moveMap["depends_on"].([]interface{}); ok {
			for _, d := range deps {
				if depStr, ok := d.(string); ok {
					dependsOn = append(dependsOn, depStr)
				}
			}
		}

		// 构建 Content
		content := map[string]interface{}{
			"instruction": instruction,
		}
		if targetRef != nil {
			content["target_ref"] = targetRef
		}
		contentJSON, _ := json.Marshal(content)

		// 创建 Move 节点
		state := worldmodel.StateOpen
		moveID := uuid.New().String()
		node := worldmodel.Node{
			ID:         moveID,
			TaskID:     taskID,
			Kind:       worldmodel.KindAction,
			Content:    contentJSON,
			State:      &state,
			Complexity: &complexity,
			DependsOn:  dependsOn,
			Priority:   int(priority),
			SourceType: "planner",
			SourceID:   "planner-agent",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		if _, err := t.world.CreateNode(ctx, node); err != nil {
			return nil, fmt.Errorf("create move node: %w", err)
		}

		created = append(created, moveID)
	}

	return map[string]interface{}{
		"created_move_ids": created,
		"count":            len(created),
	}, nil
}

// ─────────────────────────────────────────────
//  evaluate_progress 工具
// ─────────────────────────────────────────────

// EvaluateProgressTool 评估任务进展
type EvaluateProgressTool struct {
	world *worldmodel.Store
}

// NewEvaluateProgressTool 创建工具
func NewEvaluateProgressTool(world *worldmodel.Store) *EvaluateProgressTool {
	return &EvaluateProgressTool{world: world}
}

func (t *EvaluateProgressTool) Name() string { return "evaluate_progress" }

func (t *EvaluateProgressTool) Description() string {
	return "评估任务进展，判断是否应该继续生成 Move"
}

func (t *EvaluateProgressTool) Schema() provider.ToolSchema {
	return provider.ToolSchema{
		Name:        "evaluate_progress",
		Description: "评估任务进展",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {}
		}`),
	}
}

func (t *EvaluateProgressTool) Execute(ctx context.Context, input map[string]interface{}) (interface{}, error) {
	taskID, ok := ctx.Value("task_id").(string)
	if !ok {
		return nil, fmt.Errorf("task_id not in context")
	}

	// 统计各种节点数量
	openMoves, _ := t.world.ListOpenActions(ctx, taskID)
	completedMoves, _ := t.world.ListCompletedActions(ctx, taskID)
	discoveries, _ := t.world.ListFindings(ctx, taskID)
	verifiedDiscoveries, _ := t.world.ListVerifiedFindings(ctx, taskID)

	result := map[string]interface{}{
		"open_moves_count":      len(openMoves),
		"completed_moves_count": len(completedMoves),
		"discoveries_count":     len(discoveries),
		"verified_discoveries_count": len(verifiedDiscoveries),
	}

	// 简单的进展评估
	if len(openMoves) == 0 && len(completedMoves) == 0 {
		result["status"] = "stalled"
		result["recommendation"] = "需要生成初始 Move"
	} else if len(openMoves) > 0 {
		result["status"] = "in_progress"
		result["recommendation"] = "等待 Move 执行完成"
	} else if len(verifiedDiscoveries) > 0 {
		result["status"] = "productive"
		result["recommendation"] = "已有验证发现，可以继续深入探索"
	} else {
		result["status"] = "exploring"
		result["recommendation"] = "继续生成探索性 Move"
	}

	return result, nil
}
