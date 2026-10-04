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
)

// Intelligence 是基于 LLM 的智能规划器
type Intelligence struct {
	router *llm.Router
	logger zerolog.Logger
}

// NewIntelligence 创建智能规划器
func NewIntelligence(router *llm.Router, logger zerolog.Logger) *Intelligence {
	return &Intelligence{
		router: router,
		logger: logger.With().Str("component", "planner_intelligence").Logger(),
	}
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
	provider, err := i.router.For(ctx, llm.ComplexityMedium)
	if err != nil {
		return nil, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	// function_tools 白名单过滤（nil=全量，空=空集——严格白名单，与 cli_tools 语义一致）
	allow := func(name string) bool {
		if functionTools == nil {
			return true
		}
		for _, n := range functionTools {
			if n == name {
				return true
			}
		}
		return false
	}

	react := runtime.NewReActRuntime()
	if allow("observe_state") {
		_ = react.RegisterTool(NewObserveStateTool(graph))
	}
	if allow("evaluate_progress") {
		_ = react.RegisterTool(NewEvaluateProgressTool(graph))
	}

	// 图工具经 ctx 取 task_id（与 executor 的 action ctx 同一机制）
	ctx = context.WithValue(ctx, "task_id", taskID) //nolint:staticcheck // 工具侧同键读取

	// 3. 跑 ReAct：SystemPrompt 定角色与输出契约，Objective 带状态摘要
	result, err := react.Run(ctx, &runtime.ReActConfig{
		Objective:            prompt,
		SystemPrompt:         i.buildPlannerSystemPrompt(),
		LLMProvider:          provider,
		MaxIterations:        6, // 规划是短决策循环：观察→(深挖)→出规划
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

	sb.WriteString("你是一个探索规划专家。根据当前任务状态，决定下一步应该执行的操作。\n\n")

	sb.WriteString("## 核心原则：智能探索模式\n")
	sb.WriteString("- 你需要**主动判断**当前探索方向是否已充分\n")
	sb.WriteString("- 根据探索内容的实质判断，而非机械计数\n")
	sb.WriteString("- 不要在同一方向无限探索，也不要过早放弃\n\n")

	sb.WriteString("## 何时停止当前方向（should_continue=false）\n\n")
	sb.WriteString("**判断标准**（根据实际情况灵活判断）：\n\n")
	sb.WriteString("1. **内容维度**（最重要）\n")
	sb.WriteString("   - 当前方向的主要探索点已基本覆盖\n")
	sb.WriteString("   - 近期 Actions 出现重复或相似模式\n")
	sb.WriteString("   - 新 Actions 的边际收益明显递减\n")
	sb.WriteString("   - 已有发现足以支撑新的探索方向\n\n")

	sb.WriteString("2. **深度参考**（仅供参考，不是硬性要求）\n")
	sb.WriteString("   - 简单方向：5-10 个动作\n")
	sb.WriteString("   - 常规方向：10-20 个动作\n")
	sb.WriteString("   - 复杂方向：20-30+ 动作\n\n")

	sb.WriteString("**重要**：主动判断是否应该停止，不要等到无事可做！\n")
	sb.WriteString("停止后，系统会自动从 Results 中提取新的探索方向。\n\n")

	// 任务目标
	sb.WriteString("## 任务目标\n")
	if ctx.Objective != "" {
		sb.WriteString(ctx.Objective)
	} else {
		sb.WriteString("（未指定明确目标）")
	}
	sb.WriteString("\n\n")

	// 当前探索统计
	sb.WriteString("## 当前探索统计\n")
	fmt.Fprintf(&sb, "- 已完成 Actions: %d\n", len(ctx.CompletedActions))
	fmt.Fprintf(&sb, "- 进行中 Actions: %d\n", len(ctx.PendingActions))
	fmt.Fprintf(&sb, "- 已确认 Results: %d\n", len(ctx.Results))
	if len(ctx.CompletedActions) >= 20 {
		sb.WriteString("- ⚠️ 提示：当前方向已探索较深，建议评估是否应该切换方向\n")
	}
	sb.WriteString("\n")

	// 已完成的工作
	sb.WriteString("## 已完成的工作\n")
	if len(ctx.CompletedActions) > 0 {
		for _, action := range ctx.CompletedActions {
			var a struct {
				Instruction string `json:"instruction"`
			}
			if err := json.Unmarshal(action.Content, &a); err != nil {
				fmt.Fprintf(&sb, "- [完成] (解析失败: %v)\n", err)
				continue
			}
			fmt.Fprintf(&sb, "- [完成] [ID: %s] %s\n", action.ID, a.Instruction)
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
			if err := json.Unmarshal(action.Content, &a); err != nil {
				fmt.Fprintf(&sb, "- [进行中] (解析失败: %v)\n", err)
				continue
			}
			fmt.Fprintf(&sb, "- [进行中] [ID: %s] %s\n", action.ID, a.Instruction)
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
			if err := json.Unmarshal(action.Content, &a); err != nil {
				fmt.Fprintf(&sb, "- [失败] (解析失败: %v)\n", err)
				continue
			}
			reason := "未知原因"
			if action.State != nil && *action.State == explorationgraph.StateFailed {
				var fullAction struct {
					Instruction string `json:"instruction"`
					Reason      string `json:"reason"`
				}
				if err := json.Unmarshal(action.Content, &fullAction); err == nil && fullAction.Reason != "" {
					reason = fullAction.Reason
				}
			}
			fmt.Fprintf(&sb, "- [失败] [ID: %s] %s（原因：%s）\n", action.ID, a.Instruction, reason)
		}
		sb.WriteString("\n")
	}

	// 已确认的发现
	if len(ctx.Results) > 0 {
		sb.WriteString("## 已确认的发现\n")
		for _, result := range ctx.Results {
			var r struct {
				Summary string `json:"summary"`
			}
			if err := json.Unmarshal(result.Content, &r); err != nil {
				fmt.Fprintf(&sb, "- (解析失败: %v)\n", err)
				continue
			}
			fmt.Fprintf(&sb, "- %s\n", r.Summary)
		}
		sb.WriteString("\n")
	}

	// 已被复现门证伪的假设——硬约束：不要再为这些方向生成同质动作。
	if len(ctx.RefutedHypotheses) > 0 {
		sb.WriteString("## 已被证伪的假设（复现门裁决，禁止原样重提）\n")
		for _, h := range ctx.RefutedHypotheses {
			fmt.Fprintf(&sb, "- %s\n", h)
		}
		sb.WriteString("\n**注意**：以上假设均经真实复现验证被否定。除非你有实质不同的新证据或新方法，否则不要再生成同方向的攻击动作。\n\n")
	}

	// 规划要求
	sb.WriteString("## 规划要求\n\n")
	sb.WriteString("请基于以上信息，提出下一步的探索操作。\n\n")

	sb.WriteString("**继续探索的判断标准**：\n")
	sb.WriteString("- 从已有发现中寻找新的探索线索\n")
	sb.WriteString("- 对成功的操作进行深入探索\n")
	sb.WriteString("- 对失败的操作尝试替代方案\n")
	sb.WriteString("- 横向扩展到相关领域\n")
	sb.WriteString("- 只有在确实无法继续时才设置 should_continue=false\n\n")

	sb.WriteString("**Action 类型参考**：\n")
	sb.WriteString("- reconnaissance: 信息收集\n")
	sb.WriteString("- analysis: 分析研究\n")
	sb.WriteString("- verification: 验证测试\n")
	sb.WriteString("- exploration: 深度探索\n")
	sb.WriteString("- expansion: 横向扩展\n\n")

	sb.WriteString("**复杂度级别**：\n")
	sb.WriteString("- simple: 简单操作（快速执行）\n")
	sb.WriteString("- moderate: 中等复杂度（常规操作）\n")
	sb.WriteString("- complex: 复杂操作（深度分析）\n\n")

	sb.WriteString("**优先级**：\n")
	sb.WriteString("- critical: 阻塞后续工作\n")
	sb.WriteString("- high: 重要且紧急\n")
	sb.WriteString("- medium: 常规优先级\n")
	sb.WriteString("- low: 可选补充\n\n")

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
	sb.WriteString("      \"depends_on\": [\"可引用上面列出的真实 Action ID；列表里没有可依赖的就填 []，绝不要编造或照抄示例\"],\n")
	sb.WriteString("      \"metadata\": {}\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ]\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n")

	return sb.String()
}

// callLLM 调用 LLM 进行推理
// buildPlannerSystemPrompt 规划 agent 的系统提示（ReAct 形态）。
func (i *Intelligence) buildPlannerSystemPrompt() string {
	return `你是渗透测试的探索规划专家。通过 observe_state / evaluate_progress 工具了解探索图现状，然后规划下一批 Action。

**工作方式**：
1. 先调 evaluate_progress 看全局进展；信息不足再调 observe_state 深挖
2. 基于观察决定：继续探索（提出 Action）或停止（should_continue=false）
3. 规划完成后停止调用工具，**只输出一个 JSON 对象**（不要包裹 markdown）：
{"should_continue": true, "reasoning": "决策理由", "actions": [{"type": "reconnaissance|vulnerability_scan|exploitation|analysis|expansion", "instruction": "具体做什么", "complexity": "simple|moderate|complex", "priority": "critical|high|medium|low", "reason": "为什么", "depends_on": [], "metadata": {}}]}

**规划原则**：优先复现已见线索；一次 1-5 个 Action；depends_on 只引用已知 ID；已失败方向换路。`
}

// parsePlanningResponse 从 ReAct 最终答案解析规划 JSON。
func (i *Intelligence) parsePlanningResponse(content string) (*PlanningResponse, error) {
	if content == "" {
		return nil, fmt.Errorf("规划输出为空")
	}

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
	var nodes []explorationgraph.Node

	for _, proposal := range proposals {
		// 生成节点 ID
		actionID := uuid.New().String()

		// 转换复杂度
		complexity := explorationgraph.ComplexitySimple
		switch proposal.Complexity {
		case "moderate":
			complexity = explorationgraph.ComplexityModerate
		case "complex":
			complexity = explorationgraph.ComplexityComplex
		}

		// 转换优先级
		priority := explorationgraph.PriorityMedium
		switch proposal.Priority {
		case "critical":
			priority = explorationgraph.PriorityCritical
		case "high":
			priority = explorationgraph.PriorityHigh
		case "low":
			priority = explorationgraph.PriorityLow
		}

		// 过滤无效的依赖 ID——坏依赖（格式非法、或图里不存在的"幻影 ID"，如 LLM
		// 照抄 prompt 里的示例 UUID）会把 Action 永久卡 blocked，进而死锁全图。
		existing, exErr := graph.ListNodesByKind(ctx, taskID, core.KindAction)
		if exErr != nil {
			existing = nil // 查不到就不做存在性过滤，退回纯格式校验
		}
		existingIDs := make(map[string]bool, len(existing))
		for _, a := range existing {
			existingIDs[a.ID] = true
		}
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

// ExtractObjectivesFromResults 从 Result 节点中提取新的探索目标
func (i *Intelligence) ExtractObjectivesFromResults(ctx context.Context, _ *explorationgraph.Store, taskID string, results []explorationgraph.Node) ([]NewObjective, error) {
	i.logger.Info().
		Str("task_id", taskID).
		Int("result_count", len(results)).
		Msg("开始从 Result 提取探索目标")

	if len(results) == 0 {
		return []NewObjective{}, nil
	}

	// 1. 构建提取 prompt
	prompt := i.buildObjectiveExtractionPrompt(results)

	// 2. 调用 LLM
	response, err := i.callObjectiveExtractionLLM(ctx, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM 推理失败: %w", err)
	}

	// 3. 如果没有提取到新目标
	if len(response.Objectives) == 0 {
		i.logger.Info().Msg("LLM 未提取到新探索目标")
		return []NewObjective{}, nil
	}

	// 4. 转换为 NewObjective
	objectives := make([]NewObjective, 0, len(response.Objectives))
	for _, obj := range response.Objectives {
		priority := core.PriorityMedium
		switch obj.Priority {
		case "critical":
			priority = core.PriorityCritical
		case "high":
			priority = core.PriorityHigh
		case "low":
			priority = core.PriorityLow
		}

		objectives = append(objectives, NewObjective{
			Description: obj.Description,
			Priority:    priority,
			TriggeredBy: obj.TriggeredBy,
			Reasoning:   obj.Reasoning,
		})

		i.logger.Debug().
			Str("description", obj.Description).
			Str("priority", string(priority)).
			Int("triggered_by_count", len(obj.TriggeredBy)).
			Msg("提取到探索目标")
	}

	i.logger.Info().
		Int("objective_count", len(objectives)).
		Msg("成功提取探索目标")

	return objectives, nil
}

// buildObjectiveExtractionPrompt 构建目标提取 prompt
func (i *Intelligence) buildObjectiveExtractionPrompt(results []explorationgraph.Node) string {
	var sb strings.Builder

	sb.WriteString("你是一个探索规划专家。根据已有的探索结果，提取新的探索目标。\n\n")

	sb.WriteString("## 核心原则\n")
	sb.WriteString("- 这是一个持续探索的任务，永远假设还有未知领域需要探索\n")
	sb.WriteString("- 从每个结果中寻找新的线索、新的方向、新的可能性\n")
	sb.WriteString("- 深度优先：对已有发现进行深入探索\n")
	sb.WriteString("- 广度扩展：从已知点扩展到相关领域\n\n")

	sb.WriteString("## 已有探索结果\n")
	for i, result := range results {
		var content map[string]interface{}
		if err := json.Unmarshal(result.Content, &content); err != nil {
			fmt.Fprintf(&sb, "%d. [Result ID: %s] (解析失败: %v)\n\n", i+1, result.ID, err)
			continue
		}

		fmt.Fprintf(&sb, "%d. [Result ID: %s]\n", i+1, result.ID)
		if summary, ok := content["summary"].(string); ok {
			fmt.Fprintf(&sb, "   摘要: %s\n", summary)
		}
		if status, ok := content["status"].(string); ok {
			fmt.Fprintf(&sb, "   状态: %s\n", status)
		}
		sb.WriteString("\n")
	}

	sb.WriteString("## 你的任务\n")
	sb.WriteString("分析上述结果，提取新的探索目标。每个目标应该：\n")
	sb.WriteString("1. 基于某个或多个结果中的线索\n")
	sb.WriteString("2. 有明确的探索方向和理由\n")
	sb.WriteString("3. 有合理的优先级\n\n")

	sb.WriteString("## 输出格式\n")
	sb.WriteString("请以 JSON 格式输出（必须是有效的 JSON）：\n")
	sb.WriteString("```json\n")
	sb.WriteString("{\n")
	sb.WriteString("  \"objectives\": [\n")
	sb.WriteString("    {\n")
	sb.WriteString("      \"description\": \"探索目标的描述\",\n")
	sb.WriteString("      \"priority\": \"critical/high/medium/low\",\n")
	sb.WriteString("      \"reasoning\": \"为什么需要这个目标\",\n")
	sb.WriteString("      \"triggered_by\": [\"result_id_1\", \"result_id_2\"]\n")
	sb.WriteString("    }\n")
	sb.WriteString("  ]\n")
	sb.WriteString("}\n")
	sb.WriteString("```\n")

	return sb.String()
}

// ObjectiveExtractionResponse LLM 返回的目标提取结果
type ObjectiveExtractionResponse struct {
	Objectives []struct {
		Description string   `json:"description"`
		Priority    string   `json:"priority"`
		Reasoning   string   `json:"reasoning"`
		TriggeredBy []string `json:"triggered_by"`
	} `json:"objectives"`
}

// callObjectiveExtractionLLM 调用 LLM 提取目标
func (i *Intelligence) callObjectiveExtractionLLM(ctx context.Context, prompt string) (*ObjectiveExtractionResponse, error) {
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

	resp, err := provider.Complete(ctx, llm.Request{
		Messages:  messages,
		MaxTokens: 2000,
	})
	if err != nil {
		return nil, fmt.Errorf("LLM complete 失败: %w", err)
	}

	text := resp.Content
	i.logger.Debug().Str("raw_response", text).Msg("LLM 原始响应")

	// 提取 JSON（去除 markdown 代码块）
	start := strings.Index(text, "```json")
	if start != -1 {
		start += 7
		end := strings.Index(text[start:], "```")
		if end != -1 {
			text = text[start : start+end]
		}
	}

	// 解析 JSON
	var response ObjectiveExtractionResponse
	if err := json.Unmarshal([]byte(text), &response); err != nil {
		return nil, fmt.Errorf("解析 LLM 响应失败: %w, 原始响应: %s", err, text)
	}

	return &response, nil
}
