// Package planneragent 实现事件驱动的 LLM Planner Agent。
// Planner 读取世界模型状态，通过 LLM 推理产出 Move，写入 execution_plan 表。
package planneragent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/executionplan"
	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// Tool 定义 Planner Agent 可用的工具接口
type Tool interface {
	Name() string
	Description() string
	Schema() map[string]interface{}
	Execute(ctx context.Context, args map[string]interface{}) (interface{}, error)
}

// ObserveStateTool 允许 Planner 读取世界模型当前状态
type ObserveStateTool struct {
	world *worldmodel.Store
}

func NewObserveStateTool(world *worldmodel.Store) *ObserveStateTool {
	return &ObserveStateTool{world: world}
}

func (t *ObserveStateTool) Name() string {
	return "observe_state"
}

func (t *ObserveStateTool) Description() string {
	return "Read current world model state: nodes (confirmed assets, credentials, access, findings) and edges (attack chains, dependencies)."
}

func (t *ObserveStateTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "Task ID to query world model",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *ObserveStateTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	taskID, ok := args["task_id"].(string)
	if !ok || taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	nodes, err := t.world.ListNodes(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	edges, err := t.world.ListEdges(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list edges: %w", err)
	}

	return map[string]interface{}{
		"nodes": nodes,
		"edges": edges,
		"summary": map[string]interface{}{
			"node_count": len(nodes),
			"edge_count": len(edges),
		},
	}, nil
}

// ProposeMovesTool 允许 Planner 提交计划的 Move 到 execution_plan
type ProposeMovesTool struct {
	planStore *executionplan.Store
}

func NewProposeMovesTool(planStore *executionplan.Store) *ProposeMovesTool {
	return &ProposeMovesTool{planStore: planStore}
}

func (t *ProposeMovesTool) Name() string {
	return "propose_moves"
}

func (t *ProposeMovesTool) Description() string {
	return "Submit planned exploration moves to execution queue. Each move specifies: kind (enumerate/probe/exploit/escalate/persist), domain (web/binary/cloud/lateral), target, priority, and reasoning."
}

func (t *ProposeMovesTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "Task ID for the moves",
			},
			"moves": map[string]interface{}{
				"type": "array",
				"items": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"kind": map[string]interface{}{
							"type": "string",
							"enum": []string{"enumerate", "probe", "exploit", "escalate", "persist"},
							"description": "Kill chain phase",
						},
						"domain": map[string]interface{}{
							"type": "string",
							"enum": []string{"web", "binary", "cloud", "lateral"},
							"description": "Domain executor",
						},
						"target": map[string]interface{}{
							"type": "object",
							"properties": map[string]interface{}{
								"domain":  map[string]string{"type": "string"},
								"ref_kind": map[string]string{"type": "string"},
								"locator": map[string]string{"type": "string"},
							},
							"required": []string{"domain", "ref_kind", "locator"},
							"description": "Target reference (three-tuple)",
						},
						"priority": map[string]interface{}{
							"type": "integer",
							"description": "Priority (higher = more urgent, default 5)",
						},
						"reason": map[string]interface{}{
							"type": "string",
							"description": "Why this move is proposed (for audit trail)",
						},
					},
					"required": []string{"kind", "domain", "target", "reason"},
				},
			},
		},
		"required": []string{"task_id", "moves"},
	}
}

func (t *ProposeMovesTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	taskID, ok := args["task_id"].(string)
	if !ok || taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	movesRaw, ok := args["moves"].([]interface{})
	if !ok {
		return nil, fmt.Errorf("moves must be an array")
	}

	var createdIDs []string
	for _, moveRaw := range movesRaw {
		moveMap, ok := moveRaw.(map[string]interface{})
		if !ok {
			continue
		}

		// 解析 Move
		move := executionplan.Move{
			TaskID: taskID,
			Kind:   executionplan.MoveKind(getString(moveMap, "kind")),
			Domain: getString(moveMap, "domain"),
			Reason: getString(moveMap, "reason"),
		}

		// 解析 target
		if targetMap, ok := moveMap["target"].(map[string]interface{}); ok {
			move.TargetRef = worldmodel.TargetRef{
				Domain:  getString(targetMap, "domain"),
				RefKind: getString(targetMap, "ref_kind"),
				Locator: getString(targetMap, "locator"),
			}
		}

		// 解析 priority
		if priority, ok := moveMap["priority"].(float64); ok {
			move.Priority = int(priority)
		}

		// 创建 Move
		created, err := t.planStore.Create(ctx, move)
		if err != nil {
			return nil, fmt.Errorf("create move: %w", err)
		}

		createdIDs = append(createdIDs, created.ID.String())
	}

	return map[string]interface{}{
		"created_move_ids": createdIDs,
		"count":            len(createdIDs),
	}, nil
}

// EvaluateProgressTool 允许 Planner 评估当前进度
type EvaluateProgressTool struct {
	planStore *executionplan.Store
	world     *worldmodel.Store
}

func NewEvaluateProgressTool(planStore *executionplan.Store, world *worldmodel.Store) *EvaluateProgressTool {
	return &EvaluateProgressTool{
		planStore: planStore,
		world:     world,
	}
}

func (t *EvaluateProgressTool) Name() string {
	return "evaluate_progress"
}

func (t *EvaluateProgressTool) Description() string {
	return "Evaluate task progress: how many moves completed, pending moves, world model growth, and whether goal is achieved."
}

func (t *EvaluateProgressTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "Task ID to evaluate",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *EvaluateProgressTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	taskID, ok := args["task_id"].(string)
	if !ok || taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	// 查询所有 Move
	allMoves, err := t.planStore.ListAll(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list moves: %w", err)
	}

	// 统计状态
	stats := map[string]int{
		"pending":   0,
		"executing": 0,
		"completed": 0,
		"failed":    0,
	}
	for _, m := range allMoves {
		stats[string(m.Status)]++
	}

	// 查询世界模型节点
	nodes, err := t.world.ListNodes(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	// 按类型统计节点
	nodesByKind := make(map[string]int)
	for _, n := range nodes {
		nodesByKind[string(n.Kind)]++
	}

	return map[string]interface{}{
		"move_stats":     stats,
		"total_moves":    len(allMoves),
		"world_nodes":    len(nodes),
		"nodes_by_kind":  nodesByKind,
	}, nil
}

// AccessMemoryTool 允许 Planner 访问工作记忆（Redis 黑板上的 leads）
type AccessMemoryTool struct {
	leads *lead.Store
	world *worldmodel.Store
}

func NewAccessMemoryTool(leads *lead.Store, world *worldmodel.Store) *AccessMemoryTool {
	return &AccessMemoryTool{
		leads: leads,
		world: world,
	}
}

func (t *AccessMemoryTool) Name() string {
	return "access_memory"
}

func (t *AccessMemoryTool) Description() string {
	return "Access working memory (leads, observations) that haven't been promoted to world model yet."
}

func (t *AccessMemoryTool) Schema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"task_id": map[string]interface{}{
				"type":        "string",
				"description": "Task ID",
			},
			"filter_kind": map[string]interface{}{
				"type":        "string",
				"enum":        []string{"clue", "observation", "deadend", "all"},
				"description": "Filter by lead kind: clue (needs verification), observation (confirmed finding), deadend (blocked path), all (everything)",
			},
		},
		"required": []string{"task_id"},
	}
}

func (t *AccessMemoryTool) Execute(ctx context.Context, args map[string]interface{}) (interface{}, error) {
	taskID, _ := args["task_id"].(string)
	filterKind, _ := args["filter_kind"].(string)

	if taskID == "" {
		return nil, fmt.Errorf("task_id is required")
	}

	// 从世界模型获取该 Task 的所有节点，提取 host 列表
	nodes, err := t.world.ListNodes(ctx, taskID)
	if err != nil {
		return nil, fmt.Errorf("list nodes: %w", err)
	}

	// 提取所有 host（去重）
	hostSet := make(map[string]struct{})
	for _, node := range nodes {
		if node.Ref.Domain == "web" && node.Ref.RefKind == "host" {
			hostSet[node.Ref.Locator] = struct{}{}
		}
	}

	// 读取每个 host 的 leads
	result := map[string]interface{}{
		"hosts": []map[string]interface{}{},
	}

	for host := range hostSet {
		leadsByKind, err := t.leads.ReadRecent(ctx, host)
		if err != nil {
			// 单个 host 读取失败不阻塞整体
			continue
		}

		// 过滤 lead kind
		filteredLeads := make(map[string][]map[string]interface{})
		for kind, entries := range leadsByKind {
			// 应用过滤器
			if filterKind != "" && filterKind != "all" {
				if string(kind) != filterKind {
					continue
				}
			}

			// 转换为输出格式
			var items []map[string]interface{}
			for _, entry := range entries {
				items = append(items, map[string]interface{}{
					"kind":           string(entry.Kind),
					"detail":         entry.Detail,
					"executor_id":    entry.ExecutorID,
					"source_task_id": entry.SourceTaskID,
					"created_at":     entry.CreatedAt.Format(time.RFC3339),
				})
			}
			filteredLeads[string(kind)] = items
		}

		if len(filteredLeads) > 0 {
			hostData := map[string]interface{}{
				"host":  host,
				"leads": filteredLeads,
			}
			result["hosts"] = append(result["hosts"].([]map[string]interface{}), hostData)
		}
	}

	return result, nil
}

// ToolRegistry 管理所有 Planner 工具
type ToolRegistry struct {
	tools map[string]Tool
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

func (r *ToolRegistry) Register(tool Tool) {
	r.tools[tool.Name()] = tool
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *ToolRegistry) List() []Tool {
	var tools []Tool
	for _, t := range r.tools {
		tools = append(tools, t)
	}
	return tools
}

// ToLLMToolDefinitions 转换为 LLM 工具定义格式
func (r *ToolRegistry) ToLLMToolDefinitions() []map[string]interface{} {
	var defs []map[string]interface{}
	for _, tool := range r.tools {
		defs = append(defs, map[string]interface{}{
			"name":        tool.Name(),
			"description": tool.Description(),
			"input_schema": tool.Schema(),
		})
	}
	return defs
}

// ExecuteTool 执行工具调用
func (r *ToolRegistry) ExecuteTool(ctx context.Context, name string, argsJSON string) (interface{}, error) {
	tool, ok := r.Get(name)
	if !ok {
		return nil, fmt.Errorf("tool not found: %s", name)
	}

	var args map[string]interface{}
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return nil, fmt.Errorf("parse tool args: %w", err)
	}

	return tool.Execute(ctx, args)
}
