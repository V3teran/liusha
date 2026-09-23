// Package planner 实现基于 LLM 的智能规划引擎
package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// Intelligence 是基于 LLM 的智能规划器
type Intelligence struct {
	router llm.Router
	logger zerolog.Logger
}

// NewIntelligence 创建智能规划器
func NewIntelligence(router llm.Router, logger zerolog.Logger) *Intelligence {
	return &Intelligence{
		router: router,
		logger: logger.With().Str("component", "planner_intelligence").Logger(),
	}
}

// PlanningContext 规划上下文
type PlanningContext struct {
	Objective       string                    // 任务目标
	CompletedActions []knowledgegraph.Node    // 已完成的 Action
	PendingActions   []knowledgegraph.Node    // 待执行的 Action
	Results          []knowledgegraph.Node    // 已确认的结果
	FailedActions    []knowledgegraph.Node    // 失败的 Action
}

// ActionProposal LLM 返回的 Action 提案
type ActionProposal struct {
	Type        string                 `json:"type"`         // Action 类型
	Instruction string                 `json:"instruction"`  // 执行指令
	Complexity  string                 `json:"complexity"`   // 复杂度: simple/moderate/complex
	Priority    string                 `json:"priority"`     // 优先级: critical/high/medium/low
	Reason      string                 `json:"reason"`       // 规划理由
	DependsOn   []string               `json:"depends_on"`   // 依赖的 Action ID
	Metadata    map[string]interface{} `json:"metadata"`     // 额外元数据
}

// PlanningResponse LLM 响应
type PlanningResponse struct {
	ShouldContinue bool             `json:"should_continue"` // 是否应该继续执行
	Reasoning      string           `json:"reasoning"`       // 推理过程
	Actions        []ActionProposal `json:"actions"`         // 新提议的 Action
}

// Plan 基于当前知识图谱状态生成新的 Action
func (i *Intelligence) Plan(ctx context.Context, world *knowledgegraph.Store, taskID string) ([]knowledgegraph.Node, error) {
	i.logger.Info().Str("task_id", taskID).Msg("开始智能规划")
	fmt.Printf("[PLANNER-DEBUG] Intelligence.Plan ENTRY - taskID=%s\n", taskID)

	// 1. 收集规划上下文
	planCtx, err := i.gatherContext(ctx, world, taskID)
	if err != nil {
		return nil, fmt.Errorf("收集规划上下文失败: %w", err)
	}

	// 2. 构建 LLM prompt
	prompt := i.buildPlanningPrompt(planCtx)
	fmt.Printf("[PLANNER-DEBUG] Built prompt, length=%d\n", len(prompt))

	// 3. 调用 LLM 进行推理
	response, err := i.callLLM(ctx, prompt)
	fmt.Printf("[PLANNER-DEBUG] callLLM returned, err=%v\n", err)
	if err != nil {
		return nil, fmt.Errorf("LLM 推理失败: %w", err)
	}

	// 4. 如果 LLM 判断不应该继续，返回空
	if !response.ShouldContinue {
		i.logger.Info().
			Str("task_id", taskID).
			Str("reasoning", response.Reasoning).
			Msg("LLM 判断任务应该停止")
		return []knowledgegraph.Node{}, nil
	}

	// 5. 转换 LLM 提案为知识图谱节点
	nodes := i.convertProposalsToNodes(taskID, response.Actions)

	i.logger.Info().
		Str("task_id", taskID).
		Int("action_count", len(nodes)).
		Msg("智能规划完成")

	return nodes, nil
}

// gatherContext 收集规划所需的上下文信息
func (i *Intelligence) gatherContext(ctx context.Context, world *knowledgegraph.Store, taskID string) (*PlanningContext, error) {
	planCtx := &PlanningContext{}

	// 获取 Objective
	objectives, err := world.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return nil, fmt.Errorf("获取 Objective 失败: %w", err)
	}
	if len(objectives) > 0 {
		var obj struct {
			Description string `json:"description"`
		}
		if err := json.Unmarshal(objectives[0].Content, &obj); err == nil {
			planCtx.Objective = obj.Description
		}
	}

	// 获取所有 Action
	allActions, err := world.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return nil, fmt.Errorf("获取 Action 失败: %w", err)
	}

	// 按状态分类 Action
	for _, action := range allActions {
		if action.State == nil {
			continue
		}
		switch *action.State {
		case knowledgegraph.StateDone:
			planCtx.CompletedActions = append(planCtx.CompletedActions, action)
		case knowledgegraph.StateOpen, knowledgegraph.StateRunning:
			planCtx.PendingActions = append(planCtx.PendingActions, action)
		case knowledgegraph.StateFailed:
			planCtx.FailedActions = append(planCtx.FailedActions, action)
		}
	}

	// 获取已确认的结果
	results, err := world.ListNodesByKind(ctx, taskID, core.KindResult)
	if err != nil {
		return nil, fmt.Errorf("获取 Result 失败: %w", err)
	}
	planCtx.Results = results

	return planCtx, nil
}

// buildPlanningPrompt 构建规划 prompt
func (i *Intelligence) buildPlanningPrompt(ctx *PlanningContext) string {
	var sb strings.Builder

	sb.WriteString("你是一个渗透测试规划专家。根据当前任务状态，决定下一步应该执行的操作。\n\n")

	// 任务目标
	sb.WriteString("## 任务目标\n")
	if ctx.Objective != "" {
		sb.WriteString(ctx.Objective)
	} else {
		sb.WriteString("（未指定明确目标）")
	}
	sb.WriteString("\n\n")

	// 已完成的工作
	sb.WriteString("## 已完成的工作\n")
	if len(ctx.CompletedActions) > 0 {
		for _, action := range ctx.CompletedActions {
			var a struct {
				Instruction string `json:"instruction"`
			}
			json.Unmarshal(action.Content, &a)
			sb.WriteString(fmt.Sprintf("- [完成] %s\n", a.Instruction))
		}
	} else {
		sb.WriteString("（尚未完成任何操作）\n")
	}
	sb.WriteString("\n")

	// 进行中的工作
	if len(ctx.PendingActions) > 0 {
		sb.WriteString("## 进行中的工作\n")
		for _, action := range ctx.PendingActions {
			var a struct {
				Instruction string `json:"instruction"`
			}
			json.Unmarshal(action.Content, &a)
			sb.WriteString(fmt.Sprintf("- [进行中] %s\n", a.Instruction))
		}
		sb.WriteString("\n")
	}

	// 失败的尝试
	if len(ctx.FailedActions) > 0 {
		sb.WriteString("## 失败的尝试\n")
		for _, action := range ctx.FailedActions {
			var a struct {
				Instruction string `json:"instruction"`
			}
			json.Unmarshal(action.Content, &a)
			reason := "未知原因"
			if action.State != nil && *action.State == knowledgegraph.StateFailed {
				// 从 Content 中提取失败原因（如果有）
				var fullAction struct {
					Instruction string `json:"instruction"`
					Reason      string `json:"reason"`
				}
				if err := json.Unmarshal(action.Content, &fullAction); err == nil && fullAction.Reason != "" {
					reason = fullAction.Reason
				}
			}
			sb.WriteString(fmt.Sprintf("- [失败] %s（原因：%s）\n", a.Instruction, reason))
		}
		sb.WriteString("\n")
	}

	// 已确认的发现
	if len(ctx.Results) > 0 {
		sb.WriteString("## 已确认的发现\n")
		for _, result := range ctx.Results {
			var r struct {
				Title string `json:"title"`
			}
			json.Unmarshal(result.Content, &r)
			sb.WriteString(fmt.Sprintf("- %s\n", r.Title))
		}
		sb.WriteString("\n")
	}

	// 规划要求
	sb.WriteString("## 规划要求\n\n")
	sb.WriteString("请基于以上信息，判断是否应该继续执行，并提出下一步的操作。\n\n")
	sb.WriteString("**判断标准**：\n")
	sb.WriteString("- 如果任务目标已经达成，should_continue=false\n")
	sb.WriteString("- 如果所有可能的途径都已尝试且失败，should_continue=false\n")
	sb.WriteString("- 如果还有明显的下一步操作，should_continue=true\n\n")

	sb.WriteString("**Action 类型参考**：\n")
	sb.WriteString("- reconnaissance: 信息收集（端口扫描、目录枚举等）\n")
	sb.WriteString("- vulnerability_scan: 漏洞扫描\n")
	sb.WriteString("- exploitation: 漏洞利用\n")
	sb.WriteString("- privilege_escalation: 权限提升\n")
	sb.WriteString("- lateral_movement: 横向移动\n")
	sb.WriteString("- data_exfiltration: 数据获取\n\n")

	sb.WriteString("**复杂度级别**：\n")
	sb.WriteString("- simple: 简单操作（<5分钟）\n")
	sb.WriteString("- moderate: 中等复杂度（5-15分钟）\n")
	sb.WriteString("- complex: 复杂操作（>15分钟）\n\n")

	sb.WriteString("**优先级**：\n")
	sb.WriteString("- critical: 阻塞后续所有工作\n")
	sb.WriteString("- high: 重要但可并行\n")
	sb.WriteString("- medium: 常规优先级\n")
	sb.WriteString("- low: 可选优化\n\n")

	sb.WriteString("请以 JSON 格式返回你的规划：\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"should_continue\": true/false,\n")
	sb.WriteString("  \"reasoning\": \"你的推理过程\",\n")
	sb.WriteString("  \"actions\": [\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"type\": \"action类型\",\n")
	sb.WriteString("      \"instruction\": \"具体执行指令\",\n")
	sb.WriteString("      \"complexity\": \"simple/moderate/complex\",\n")
	sb.WriteString("      \"priority\": \"critical/high/medium/low\",\n")
	sb.WriteString("      \"reason\": \"为什么需要这个操作\",\n")
	sb.WriteString("      \"depends_on\": [\"依赖的action_id列表\"],\n")
	sb.WriteString("      \"metadata\": {}\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ]\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n")

	return sb.String()
}

// callLLM 调用 LLM 进行推理
func (i *Intelligence) callLLM(ctx context.Context, prompt string) (*PlanningResponse, error) {
	provider, err := i.router.For(ctx, "medium")
	if err != nil {
		return nil, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	messages := []llm.Message{
		{
			Role:    llm.RoleUser,
			Content: prompt,
		},
	}

	// 调用 LLM
	resp, err := provider.Complete(ctx, llm.Request{
		Messages:  messages,
		MaxTokens: 4000,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM 调用失败: %w", err)
	}

	// 解析响应
	content := resp.Content

	// 提取 JSON（可能被 ```json ``` 包裹）
	jsonStart := strings.Index(content, "{")
	jsonEnd := strings.LastIndex(content, "}")
	if jsonStart == -1 || jsonEnd == -1 {
		return nil, fmt.Errorf("LLM 响应不包含有效 JSON")
	}
	jsonContent := content[jsonStart : jsonEnd+1]

	var response PlanningResponse
	if err := json.Unmarshal([]byte(jsonContent), &response); err != nil {
		i.logger.Error().
			Err(err).
			Str("llm_response", content).
			Msg("解析 LLM 响应失败")
		return nil, fmt.Errorf("解析 LLM 响应失败: %w", err)
	}

	return &response, nil
}

// convertProposalsToNodes 将 LLM 提案转换为知识图谱节点
func (i *Intelligence) convertProposalsToNodes(taskID string, proposals []ActionProposal) []knowledgegraph.Node {
	var nodes []knowledgegraph.Node

	for _, proposal := range proposals {
		// 生成节点 ID
		actionID := uuid.New().String()

		// 转换复杂度
		complexity := knowledgegraph.ComplexitySimple
		switch proposal.Complexity {
		case "moderate":
			complexity = knowledgegraph.ComplexityModerate
		case "complex":
			complexity = knowledgegraph.ComplexityComplex
		}

		// 转换优先级
		priority := knowledgegraph.PriorityMedium
		switch proposal.Priority {
		case "critical":
			priority = knowledgegraph.PriorityCritical
		case "high":
			priority = knowledgegraph.PriorityHigh
		case "low":
			priority = knowledgegraph.PriorityLow
		}

		// 构建 Action 内容
		actionContent := map[string]interface{}{
			"type":        proposal.Type,
			"instruction": proposal.Instruction,
			"complexity":  complexity,
			"priority":    priority,
			"reason":      proposal.Reason,
			"depends_on":  proposal.DependsOn,
			"metadata":    proposal.Metadata,
		}

		contentBytes, _ := json.Marshal(actionContent)

		openState := knowledgegraph.StateOpen
		node := knowledgegraph.Node{
			ID:         actionID,
			TaskID:     taskID,
			Kind:       core.KindAction,
			Content:    contentBytes,
			State:      &openState,
			Priority:   priority,
			Complexity: &complexity,
			DependsOn:  proposal.DependsOn,
			SourceType: knowledgegraph.SourcePlanner,
			SourceID:   "planner",
			CreatedAt:  time.Now(),
		}

		nodes = append(nodes, node)

		i.logger.Debug().
			Str("action_id", actionID).
			Str("type", proposal.Type).
			Str("priority", string(priority)).
			Msg("生成 Action 节点")
	}

	return nodes
}
