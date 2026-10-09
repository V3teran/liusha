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

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/registry"
)

// Intelligence 是基于 LLM 的智能规划器
type Intelligence struct {
	router     *llm.Router
	logger     zerolog.Logger
	charter    string         // 角色章程（agent.system_prompt，运维经前端可调；空=不渲染）
	complexity llm.Complexity // LLM 档位（agent.complexity，文档 complexity 种子 → 三级缓存读）
	maxIt      int            // ReAct 迭代上限（agent.max_iterations；0=不设限）
}

// NewIntelligence 创建智能规划器
func NewIntelligence(router *llm.Router, logger zerolog.Logger) *Intelligence {
	return &Intelligence{
		router: router,
		logger: logger.With().Str("component", "planner_intelligence").Logger(),
	}
}

// WithSystemPrompt 注入角色章程（agent.system_prompt 正文，前端可编辑）。
func (i *Intelligence) WithSystemPrompt(charter string) *Intelligence {
	i.charter = charter
	return i
}

// WithMaxIterations 注入 ReAct 迭代上限（agent.max_iterations；0=不设限）。
func (i *Intelligence) WithMaxIterations(n int) *Intelligence {
	i.maxIt = n
	return i
}

// WithComplexity 注入 LLM 档位（agent.complexity，文档 complexity 种子经三级缓存读；
// 空回退 medium）。
func (i *Intelligence) WithComplexity(complexity string) *Intelligence {
	i.complexity = llm.ParseComplexity(complexity)
	return i
}

// PlanningContext 规划上下文
type PlanningContext struct {
	Objective         string                  // 任务目标
	CompletedActions  []explorationgraph.Node // 已完成的 Action
	PendingActions    []explorationgraph.Node // 待执行的 Action
	Results           []explorationgraph.Node // 已确认的结果
	FailedActions     []explorationgraph.Node // 失败的 Action
	RefutedHypotheses []string                // 已被复现门证伪的假设陈述（防止复读）
}

// ActionProposal LLM 返回的 Action 提案
type ActionProposal struct {
	Type        string                 `json:"type"`        // Action 类型
	Instruction string                 `json:"instruction"` // 执行指令
	Complexity  string                 `json:"complexity"`  // 复杂度: simple/moderate/complex
	Priority    string                 `json:"priority"`    // 优先级: critical/high/medium/low
	Reason      string                 `json:"reason"`      // 规划理由
	DependsOn   []string               `json:"depends_on"`  // 依赖的 Action ID
	Metadata    map[string]interface{} `json:"metadata"`    // 额外元数据
}

// PlanningResponse LLM 响应
type PlanningResponse struct {
	ShouldContinue bool             `json:"should_continue"` // 是否应该继续执行
	Reasoning      string           `json:"reasoning"`       // 推理过程
	Actions        []ActionProposal `json:"actions"`         // 新提议的 Action
}

// Plan 基于当前探索图状态生成新的 Action——ReAct 形态：
// LLM 经 observe_state/evaluate_progress 工具自主观察探索图，思考后输出规划 JSON。
// 落图仍由 agent.go 单点执行（转换 + 依赖过滤 + 事件发布），ReAct 只负责决策。
func (i *Intelligence) Plan(ctx context.Context, graph *explorationgraph.Store, taskID string, functionTools []string) ([]explorationgraph.Node, error) {
	i.logger.Info().Str("task_id", taskID).Msg("开始智能规划")

	// 1. 收集规划上下文（作为首条输入的紧凑摘要；细节靠工具按需深挖）
	planCtx, err := i.gatherContext(ctx, graph, taskID)
	if err != nil {
		return nil, fmt.Errorf("收集规划上下文失败: %w", err)
	}
	prompt := i.buildPlanningPrompt(planCtx)

	// 2. 组装 ReAct 规划器：observe_state / evaluate_progress 只读图工具
	provider, err := i.router.For(ctx, i.complexity)
	if err != nil {
		return nil, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	// function_tools 白名单过滤（nil=全量，空=空集——严格白名单，与 cli_tools 语义一致）
	react := runtime.NewReActRuntime()
	if registry.Allows(functionTools, "observe_state") {
		if err := react.RegisterTool(NewObserveStateTool(graph)); err != nil {
			return nil, fmt.Errorf("注册 observe_state 失败: %w", err)
		}
	}
	if registry.Allows(functionTools, "evaluate_progress") {
		if err := react.RegisterTool(NewEvaluateProgressTool(graph)); err != nil {
			return nil, fmt.Errorf("注册 evaluate_progress 失败: %w", err)
		}
	}

	// 图工具经 ctx 取 task_id（类型化 key；工具侧用 TaskIDFromContext 读取）
	ctx = WithTaskID(ctx, taskID)

	// 3. 跑 ReAct：SystemPrompt 定角色与输出契约，Objective 带状态摘要
	result, err := react.Run(ctx, &runtime.ReActConfig{
		Objective:            prompt,
		SystemPrompt:         i.buildPlannerSystemPrompt(),
		LLMProvider:          provider,
		MaxIterations:        i.maxIt, // 迭代上限 = agent.max_iterations（配置即事实；0=不设限）
		MaxTokens:            4000,
		MessageModifierChain: runtime.NewDefaultModifierChain(20),
	})
	if err != nil {
		return nil, fmt.Errorf("ReAct 规划失败: %w", err)
	}

	// 4. 解析最终答案为 PlanningResponse
	response, err := i.parsePlanningResponse(result.FinalAnswer)
	if err != nil {
		return nil, fmt.Errorf("解析规划输出失败: %w", err)
	}

	// 5. 如果 LLM 判断不应该继续，返回空
	if !response.ShouldContinue {
		i.logger.Info().
			Str("task_id", taskID).
			Str("reasoning", response.Reasoning).
			Msg("LLM 判断任务应该停止")
		return []explorationgraph.Node{}, nil
	}

	// 6. 转换 LLM 提案为探索图节点
	nodes := i.convertProposalsToNodes(ctx, graph, taskID, response.Actions)

	i.logger.Info().
		Str("task_id", taskID).
		Int("action_count", len(nodes)).
		Msg("智能规划完成")

	return nodes, nil
}

// gatherContext 收集规划所需的上下文信息
func (i *Intelligence) gatherContext(ctx context.Context, graph *explorationgraph.Store, taskID string) (*PlanningContext, error) {
	planCtx := &PlanningContext{}

	// 获取 Objective
	objectives, err := graph.ListNodesByKind(ctx, taskID, core.KindObjective)
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
	allActions, err := graph.ListNodesByKind(ctx, taskID, core.KindAction)
	if err != nil {
		return nil, fmt.Errorf("获取 Action 失败: %w", err)
	}

	// 按状态分类 Action
	for _, action := range allActions {
		if action.State == nil {
			continue
		}
		switch *action.State {
		case explorationgraph.StateDone:
			planCtx.CompletedActions = append(planCtx.CompletedActions, action)
		case explorationgraph.StateOpen, explorationgraph.StateRunning:
			planCtx.PendingActions = append(planCtx.PendingActions, action)
		case explorationgraph.StateFailed:
			planCtx.FailedActions = append(planCtx.FailedActions, action)
		}
	}

	// 获取已确认的结果
	results, err := graph.ListNodesByKind(ctx, taskID, core.KindResult)
	if err != nil {
		return nil, fmt.Errorf("获取 Result 失败: %w", err)
	}
	planCtx.Results = results

	// 获取已被复现门证伪的假设（evaluator 的 verification_outcome 标记）——
	// 规划器对"哪条路已试死"的权威事实源，防同方向反复重提（复读机）。
	refutedObservations, err := graph.ListNodesByKind(ctx, taskID, core.KindObservation)
	if err != nil {
		return nil, fmt.Errorf("获取 Observation 失败: %w", err)
	}
	for _, obs := range refutedObservations {
		if obs.Metadata == nil {
			continue
		}
		var m struct {
			VerificationOutcome string `json:"verification_outcome"`
		}
		if json.Unmarshal(obs.Metadata, &m) != nil || m.VerificationOutcome != "refuted" {
			continue
		}
		var c struct {
			Statement string `json:"statement"`
		}
		if json.Unmarshal(obs.Content, &c) == nil && c.Statement != "" {
			planCtx.RefutedHypotheses = append(planCtx.RefutedHypotheses, c.Statement)
		}
	}

	return planCtx, nil
}

// buildPlanningPrompt 构建规划 prompt
func (i *Intelligence) buildPlanningPrompt(ctx *PlanningContext) string {
	var sb strings.Builder

	// 1. 头部和核心原则
	sb.WriteString(buildPlanningPromptHeader())

	// 2. 停止条件
	sb.WriteString(buildStopCriteria())

	// 3. 任务目标
	sb.WriteString(buildObjectiveSection(ctx.Objective))

	// 4. 统计信息
	sb.WriteString(buildStatisticsSection(ctx))

	// 5. 已完成的工作
	sb.WriteString(buildCompletedActionsSection(ctx.CompletedActions))

	// 6. 进行中的工作
	sb.WriteString(buildPendingActionsSection(ctx.PendingActions))

	// 7. 失败的尝试
	sb.WriteString(buildFailedActionsSection(ctx.FailedActions))

	// 8. 已确认的发现
	sb.WriteString(buildResultsSection(ctx.Results))

	// 9. 已被证伪的假设
	sb.WriteString(buildRefutedHypothesesSection(ctx.RefutedHypotheses))

	// 10. 规划要求
	sb.WriteString(buildPlanningRequirements())

	// 11. JSON 格式示例
	sb.WriteString(buildJSONSchema())

	return sb.String()
}

// buildPlannerSystemPrompt 组装规划 system prompt：
// 角色章程（agent.system_prompt——DB 事实源，种子 = agents/planner.md 正文，前端可调）
// + 空正文单句兜底。输出 JSON 契约由章程承载，改契约 = 改 agents/planner.md + make reseed。
func (i *Intelligence) buildPlannerSystemPrompt() string {
	if c := strings.TrimSpace(i.charter); c != "" {
		return c
	}
	return "你是渗透测试的探索规划专家：基于上下文摘要规划下一批 Action" +
		"（1-5 个，depends_on 只引用已知 ID），输出 JSON：" +
		`{"should_continue": true, "reasoning": "...", "actions": [{"type": "...", "instruction": "...", "complexity": "...", "priority": "...", "reason": "...", "depends_on": [], "metadata": {}}]}`
}

// parsePlanningResponse 从 ReAct 最终答案解析规划 JSON。
func (i *Intelligence) parsePlanningResponse(content string) (*PlanningResponse, error) {
	if content == "" {
		return nil, fmt.Errorf("规划输出为空")
	}

	jsonContent := llm.ExtractJSON(content)
	if jsonContent == "" {
		return nil, fmt.Errorf("LLM 响应不包含有效 JSON")
	}

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

// filterValidDependencies 过滤依赖 ID 中的非法值——LLM 可能编造非 UUID 的依赖
// （如 "0"），或照抄 prompt 里的示例 UUID（格式合法但图中不存在）；两者都会把
// Action 永久卡在 blocked。existingIDs 为图中已知 action ID 集（nil 跳过存在性校验）。
func filterValidDependencies(deps []string, existingIDs map[string]bool) []string {
	valid := make([]string, 0, len(deps))
	for _, depID := range deps {
		if _, err := uuid.Parse(depID); err != nil {
			continue
		}
		if existingIDs != nil && !existingIDs[depID] {
			continue
		}
		valid = append(valid, depID)
	}
	return valid
}

// convertProposalsToNodes 将 LLM 提案转换为探索图节点
func (i *Intelligence) convertProposalsToNodes(ctx context.Context, graph *explorationgraph.Store, taskID string, proposals []ActionProposal) []explorationgraph.Node {
	// 过滤无效的依赖 ID——坏依赖（格式非法、或图里不存在的"幻影 ID"，如 LLM
	// 照抄 prompt 里的示例 UUID）会把 Action 永久卡 blocked，进而死锁全图。
	// 已知 action 集整批查一次（查不到就不做存在性过滤，退回纯格式校验）。
	existing, exErr := graph.ListNodesByKind(ctx, taskID, core.KindAction)
	if exErr != nil {
		i.logger.Warn().Err(exErr).Str("task_id", taskID).
			Msg("查询已有 Action 失败，本轮跳过依赖存在性过滤（仅格式校验）")
		existing = nil
	}
	existingIDs := make(map[string]bool, len(existing))
	for _, a := range existing {
		existingIDs[a.ID] = true
	}

	var nodes []explorationgraph.Node
	for _, proposal := range proposals {
		// 生成节点 ID
		actionID := uuid.New().String()

		complexity := explorationgraph.NormalizeComplexity(proposal.Complexity)
		priority := explorationgraph.NormalizePriority(proposal.Priority)

		validDependsOn := filterValidDependencies(proposal.DependsOn, existingIDs)
		if len(validDependsOn) < len(proposal.DependsOn) {
			for _, depID := range proposal.DependsOn {
				if _, pErr := uuid.Parse(depID); pErr != nil {
					i.logger.Warn().Str("invalid_dep_id", depID).Str("action_id", actionID).
						Msg("过滤掉无效的依赖 ID（格式非法）")
				} else if !existingIDs[depID] {
					i.logger.Warn().Str("phantom_dep_id", depID).Str("action_id", actionID).
						Msg("过滤掉幻影依赖 ID（图中不存在——多为 LLM 照抄 prompt 示例）")
				}
			}
		}

		// 构建 Action 内容
		actionContent := map[string]interface{}{
			"type":        proposal.Type,
			"instruction": proposal.Instruction,
			"complexity":  complexity,
			"priority":    priority,
			"reason":      proposal.Reason,
			"depends_on":  validDependsOn,
			"metadata":    proposal.Metadata,
		}

		contentBytes, _ := json.Marshal(actionContent)

		openState := explorationgraph.StateOpen
		node := explorationgraph.Node{
			ID:         actionID,
			TaskID:     taskID,
			Kind:       core.KindAction,
			Content:    contentBytes,
			State:      &openState,
			Priority:   priority,
			Complexity: &complexity,
			DependsOn:  validDependsOn,
			SourceType: explorationgraph.SourcePlanner,
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
