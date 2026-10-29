// Package planner 实现基于 LLM 的智能规划引擎
package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
)

// Intelligence 是基于 LLM 的智能规划器
type Intelligence struct {
	router     *llm.Router
	logger     zerolog.Logger
	charter    string         // 角色章程（agent.system_prompt，运维经前端可调；空=不渲染）
	complexity llm.Complexity // LLM 档位（agent.complexity，文档 complexity 种子 → 三级缓存读）
	thoughts   reasoning.Tap  // 思考流出口（认知循环注入；nil=不流式）
	maxIt      int            // ReAct 迭代上限（agent.max_iterations；0=不设限）
	// messageWindow 是 ReAct 滚动窗口条数（系统配置 compaction 组，任务开始时快照；
	// 0=用 NewDefaultModifierChain 内置默认 20）。
	messageWindow int
	timeout       time.Duration // 单次规划超时基准（0=不设限；默认 3 分钟）
	budget        time.Duration // 当前生效的规划预算：随连续 deadline 失败自适应放大，成功后回落
	roster        []AgentBrief  // 四 Agent 名册（name + 调度摘要；空=规划 prompt 不带该节）

	// insightsDigest 是跨 agent 经验黑板的摘要出口（cognition 注入闭包；
	// nil=无黑板）。黑板的核心价值是规划者消费执行者的经验——没有它，
	// 黑板退化成 executor 的自说自话。
	insightsDigest func(ctx context.Context) []string
}

// AgentBrief 是团队名册的一行：code 寻址键 + 展示名 + 调度摘要。
// 调度摘要是各角色配置里面向编排者的分工描述（agent.description，前端可编辑）——
// planner 规划/派发动作时据此理解团队分工，是 LLM 可读面。
type AgentBrief struct {
	Code        string
	Name        string
	Description string
}

// WithAgentRoster 注入四 Agent 名册（cognition 装配时从 DB 配置取；空=不渲染该节）。
func (i *Intelligence) WithAgentRoster(roster []AgentBrief) *Intelligence {
	i.roster = roster
	return i
}

// WithInsightsDigest 注入经验黑板摘要出口（cognition 闭包：assignment 内
// insight 按优先级取顶，格式化为一行一条）。nil=无黑板，规划上下文不带该节。
func (i *Intelligence) WithInsightsDigest(fn func(ctx context.Context) []string) *Intelligence {
	i.insightsDigest = fn
	return i
}

// maxBudgetFactor 是自适应预算的放大上限（基准的倍数）——无界放大等于取消保护。
const maxBudgetFactor = 3

// NewIntelligence 创建智能规划器
func NewIntelligence(router *llm.Router, logger zerolog.Logger) *Intelligence {
	return &Intelligence{
		router:  router,
		logger:  logger.With().Str("component", "planner_intelligence").Logger(),
		timeout: 3 * time.Minute, // 默认单次规划超时 3 分钟
		budget:  3 * time.Minute,
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

// WithMessageWindow 注入 ReAct 滚动窗口条数（系统配置 compaction 组；0=内置默认 20）。
func (i *Intelligence) WithMessageWindow(n int) *Intelligence {
	i.messageWindow = n
	return i
}

// WithComplexity 注入 LLM 档位（agent.complexity，文档 complexity 种子经三级缓存读；
// 空回退 medium）。
func (i *Intelligence) WithComplexity(complexity string) *Intelligence {
	i.complexity = llm.ParseComplexity(complexity)
	return i
}

// WithThoughts 注入思考流出口（认知循环的会话流实现；nil=不流式）。
func (i *Intelligence) WithThoughts(tap reasoning.Tap) *Intelligence {
	i.thoughts = tap
	return i
}

// WithTimeout 注入单次规划超时（0=不设限；默认 3 分钟）。
func (i *Intelligence) WithTimeout(timeout time.Duration) *Intelligence {
	i.timeout = timeout
	return i
}

// RefutedHypothesis 是被复现门证伪的假设：反驳原因区分"证据没拿到"
// （not_reproducible/invalid_recipe——配方或上下文问题）与"拿到了证据但
// 主张不成立"（claim_invalid/assert_not_discriminative——语义/质量问题），
// 规划器据此决定换配方重试还是放弃方向。
type RefutedHypothesis struct {
	Statement string
	Reason    string
}

// Fact 是已确认的世界状态断言（观察的终态类别）：会话已建立、某机制由
// 什么控制、资源是否存在——规划的基底，直接消费、无需验证。
type Fact struct {
	Statement string
}

// PlanningContext 规划上下文
type PlanningContext struct {
	Objective         string                  // 根目标描述（任务目标）
	Objectives        []explorationgraph.Node // 全部目标（含派生方向）——规划面向全图
	CompletedActions  []explorationgraph.Node // 已完成的 Action
	PendingActions    []explorationgraph.Node // 待执行的 Action
	Results           []explorationgraph.Node // 已确认的结果（经裁决坐实的主张）
	FailedActions     []explorationgraph.Node // 失败的 Action
	AbortedActions    []explorationgraph.Node // 被外部纠偏中止的 Action（monitor kill；含 blocked_reason，禁止等效重提）
	RefutedHypotheses []RefutedHypothesis     // 已被复现门证伪的假设（含反驳原因，防止复读）
	Facts             []Fact                  // 已确认的世界状态（规划基底，直接消费）
	Blackboard        []string                // 跨 agent 经验黑板摘要（assignment 级 insight，规划者消费执行者经验）
}

// ActionProposal LLM 返回的 Action 提案
type ActionProposal struct {
	Type        string                 `json:"type"`         // Action 类型
	Instruction string                 `json:"instruction"`  // 执行指令
	Complexity  string                 `json:"complexity"`   // 复杂度: simple/moderate/complex
	Priority    string                 `json:"priority"`     // 优先级: critical/high/medium/low
	Reason      string                 `json:"reason"`       // 规划理由
	DependsOn   []string               `json:"depends_on"`   // 依赖的 Action ID
	TriggeredBy []string               `json:"triggered_by"` // 滋生本动作的 Result ID（发现→动作因果溯源；可选，仅在真实成立时声明）
	Metadata    map[string]interface{} `json:"metadata"`     // 额外元数据
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
	i.logger.Info().Msg("开始智能规划")

	// 超时保护（防止 LLM 调用无限挂起）+ 自适应预算：deadline 型失败（LLM 慢速期
	// 预算耗尽）用同一预算重试是确定性失败——预算随连续超时翻倍、封顶基准
	// maxBudgetFactor 倍，任一次成功即回落。调用方的重试阶梯只退避间隔，
	// 退避不了预算本身。
	if i.budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, i.budget)
		defer cancel()
		i.logger.Debug().
			Str("timeout", i.budget.String()).
			Msg("规划超时保护已启用")
	}

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

	// function_tools 白名单过滤（nil=拒绝，[]=空集，["*"]=全量——严格白名单，与 cli_tools 语义一致）
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

	// 构建中间件链：防止重复规划、无进展、目标偏离
	middlewareChain := runtime.NewMiddlewareChain(
		runtime.NewLoopDetectionMiddleware(8, 6),                 // 连续 8 次相同工具 / 6 次相似思考
		runtime.NewProgressTrackingMiddleware(12),                // 12 轮无新输出终止（连续失败止损由 runtime 内置，阈值随下下发）
		runtime.NewGoalAlignmentMiddleware(planCtx.Objective, 5), // 5 次偏离目标
	)

	// 3. 跑 ReAct：SystemPrompt 定角色与输出契约，Objective 带状态摘要。
	// 思考流出口（nil=不流式）：方法值不能从 nil 接口取，先守卫。
	var onThoughtDelta, onThinkingDelta func(string)
	if i.thoughts != nil {
		onThoughtDelta = i.thoughts.Delta
		onThinkingDelta = i.thoughts.ThinkingDelta
	}
	result, err := react.Run(ctx, &runtime.ReActConfig{
		Objective:                  prompt,
		SystemPrompt:               i.buildPlannerSystemPrompt(),
		LLMProvider:                provider,
		MaxIterations:              i.maxIt, // 迭代上限 = agent.max_iterations（配置即事实；0=不设限）
		MaxConsecutiveToolFailures: 8,       // 连续工具失败止损阈值（单机制：runtime 内置，随装配按角色调参）
		OnThoughtDelta:             onThoughtDelta,
		OnThinkingDelta:            onThinkingDelta,
		MaxTokens:                  4000,
		MessageModifierChain:       runtime.NewDefaultModifierChain(i.messageWindow),
		MiddlewareChain:            middlewareChain,
		// Planner 的结构化输出格式
		OutputFormatPrompt: "你是规划者（Planner），需要输出下一批要执行的Action。",
		OutputFormatSchema: `请输出以下JSON格式：

` + "```json\n" + `{
  "should_continue": true,
  "reasoning": "为什么需要继续/停止",
  "actions": [
    {
      "type": "reconnaissance/analysis/testing",
      "instruction": "具体要做什么",
      "complexity": "trivial/moderate/complex",
      "priority": "low/medium/high/critical",
      "depends_on": [],
      "triggered_by": []
    }
  ]
}
` + "```\n\n" + `说明：
- should_continue: 如果任务还未完成，设为true；如果已完成目标或无法继续，设为false
- actions: 要执行的action列表，如果should_continue=false则为空数组[]
- 每个action必须包含type、instruction和complexity
- depends_on: 本动作须等待完成的其它 action 的 ID（来自上文动作清单）
- triggered_by: 滋生本动作的已确认发现（Result）的 ID——动作若源于某个已坐实发现（它启发了方向，或它产出的会话/凭据/入口使本动作可行），填其 ID；因果不成立就不填，宁缺勿假`,
	})
	if err != nil {
		i.adaptBudget(err)
		return nil, fmt.Errorf("ReAct 规划失败: %w", err)
	}
	i.budget = i.timeout // 成功即回落基准预算

	// 4. 解析最终答案为 PlanningResponse。终态叙述在解析边界完成：协议转人读
	//    摘要作正文（谁解析谁叙述，前端不解释协议；协议原文由审计面保存）；
	//    解析失败时正文回退原文——终态帧始终落库，清空气泡语义不受解析成败影响。
	response, perr := i.parsePlanningResponse(result.FinalAnswer)
	round := reasoning.RoundOf(result)
	if perr == nil && response != nil {
		round.Summary = planningNarrative(*response)
	}
	if i.thoughts != nil {
		i.thoughts.Done(round)
	}
	if perr != nil {
		return nil, fmt.Errorf("解析规划输出失败: %w", perr)
	}

	// 5. 如果 LLM 判断不应该继续，返回空
	if !response.ShouldContinue {
		i.logger.Info().
			Str("reasoning", response.Reasoning).
			Msg("LLM 判断任务应该停止")
		return []explorationgraph.Node{}, nil
	}

	// 6. 转换 LLM 提案为探索图节点
	nodes := i.convertProposalsToNodes(ctx, graph, taskID, response.Actions)

	i.logger.Info().
		Int("action_count", len(nodes)).
		Msg("智能规划完成")

	return nodes, nil
}

// gatherContext 收集规划所需的上下文信息
func (i *Intelligence) gatherContext(ctx context.Context, graph *explorationgraph.Store, taskID string) (*PlanningContext, error) {
	planCtx := &PlanningContext{}

	// 全部目标（根目标 + 派生方向）都进规划视野
	objectives, err := graph.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return nil, fmt.Errorf("获取 Objective 失败: %w", err)
	}
	planCtx.Objectives = objectives
	// 根目标描述（创建序最早 = 任务入口创建的根任务目标）
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
		case explorationgraph.StateAborted:
			// 外部纠偏（monitor kill）的中止动作必须进规划视野：携带 blocked_reason
			// 呈现"这条路被砍及原因"，否则规划者失明、重规划会原样再派等效动作。
			planCtx.AbortedActions = append(planCtx.AbortedActions, action)
		}
	}

	// 获取已确认的结果
	results, err := graph.ListNodesByKind(ctx, taskID, core.KindResult)
	if err != nil {
		return nil, fmt.Errorf("获取 Result 失败: %w", err)
	}
	planCtx.Results = results

	// 观察一次取全量，fact（世界状态）与 refuted（已证伪假设）两视图共用——
	// 避免同表连查两次。
	observations, err := graph.ListNodesByKind(ctx, taskID, core.KindObservation)
	if err != nil {
		return nil, fmt.Errorf("获取 Observation 失败: %w", err)
	}
	// 已确认的世界状态（fact 观察是写入即终态，无流转）——规划的基底：
	// 已建立的会话、已探明的机制、已确认的资源，直接拿来用。
	for _, obs := range observations {
		if obs.Confidence == nil || *obs.Confidence != explorationgraph.ConfidenceFactual {
			continue
		}
		var c struct {
			Statement string `json:"statement"`
		}
		if json.Unmarshal(obs.Content, &c) == nil && c.Statement != "" {
			planCtx.Facts = append(planCtx.Facts, Fact{Statement: c.Statement})
		}
	}

	// 获取已被复现门证伪的假设（evaluator 的 verification_outcome 标记）——
	// 规划器对"哪条路已试死"的权威事实源，防同方向反复重提（复读机）。
	// refute_reason 一并带出：复现失败类可换配方重试，语义证伪类才是方向作废。
	for _, obs := range observations {
		if obs.Metadata == nil {
			continue
		}
		var m struct {
			VerificationOutcome string `json:"verification_outcome"`
			RefuteReason        string `json:"refute_reason"`
		}
		if json.Unmarshal(obs.Metadata, &m) != nil || m.VerificationOutcome != string(explorationgraph.OutcomeRefuted) {
			continue
		}
		var c struct {
			Statement string `json:"statement"`
		}
		if json.Unmarshal(obs.Content, &c) == nil && c.Statement != "" {
			planCtx.RefutedHypotheses = append(planCtx.RefutedHypotheses, RefutedHypothesis{
				Statement: c.Statement,
				Reason:    m.RefuteReason,
			})
		}
	}

	// 跨 agent 经验黑板摘要（assignment 级 insight）——规划者消费执行者经验。
	if i.insightsDigest != nil {
		planCtx.Blackboard = i.insightsDigest(ctx)
	}

	return planCtx, nil
}

// buildPlanningPrompt 构建规划 prompt
func (i *Intelligence) buildPlanningPrompt(ctx *PlanningContext) string {
	var sb strings.Builder

	// 1. 头部和核心原则
	sb.WriteString(buildPlanningPromptHeader())

	// 1.5 多智能体分工名册（各角色调度摘要，agent.description 前端可编辑）
	sb.WriteString(buildTeamRosterSection(i.roster))

	// 2. 停止条件
	sb.WriteString(buildStopCriteria())

	// 3. 任务目标
	sb.WriteString(buildObjectiveSection(ctx.Objective, ctx.Objectives))

	// 4. 统计信息
	sb.WriteString(buildStatisticsSection(ctx))

	// 5. 已完成的工作
	sb.WriteString(buildCompletedActionsSection(ctx.CompletedActions))

	// 6. 进行中的工作
	sb.WriteString(buildPendingActionsSection(ctx.PendingActions))

	// 7. 失败的尝试
	sb.WriteString(buildFailedActionsSection(ctx.FailedActions))

	// 7.5 被外部纠偏中止的动作（禁止等效重提）
	sb.WriteString(buildAbortedActionsSection(ctx.AbortedActions))

	// 8. 已确认的发现
	sb.WriteString(buildResultsSection(ctx.Results))

	// 8.5 已确认的世界状态（规划基底）
	sb.WriteString(buildFactsSection(ctx.Facts))

	// 8.6 经验黑板（跨 agent 已确认经验）
	sb.WriteString(buildBlackboardSection(ctx.Blackboard))

	// 9. 已被证伪的假设
	sb.WriteString(buildRefutedHypothesesSection(ctx.RefutedHypotheses))

	// 10. 规划要求
	sb.WriteString(buildPlanningRequirements())

	// 11. JSON 格式示例
	sb.WriteString(buildJSONSchema())

	return sb.String()
}

// buildPlannerSystemPrompt 组装规划 system prompt：
// 角色章程（agent.system_prompt——DB 事实源，种子 = agents/planner.md 正文，前端可调）。
// 输出 JSON 契约由章程承载，改契约 = 改 agents/planner.md + make reseed。
// charter 为空时返回空串，让 ReAct.Run 报错，强制运维配置。
func (i *Intelligence) buildPlannerSystemPrompt() string {
	charter := strings.TrimSpace(i.charter)
	if charter == "" {
		return "" // 让 ReAct.Run 报错，强制运维配置
	}
	return charter
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

// adaptBudget 在 deadline 型失败后放大下一轮规划预算（翻倍，封顶基准
// maxBudgetFactor 倍）；非 deadline 失败不动预算。Intelligence 由单个
// planner goroutine 串行使用，无并发竞争。
func (i *Intelligence) adaptBudget(err error) {
	if !errors.Is(err, context.DeadlineExceeded) || i.timeout <= 0 {
		return
	}
	ceiling := i.timeout * maxBudgetFactor
	if i.budget < ceiling {
		i.budget *= 2
		if i.budget > ceiling {
			i.budget = ceiling
		}
	}
	i.logger.Warn().
		Str("budget", i.budget.String()).
		Msg("规划预算耗尽（LLM 慢速期），下一轮预算已放大")
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
		i.logger.Warn().Err(exErr).
			Msg("查询已有 Action 失败，本轮跳过依赖存在性过滤（仅格式校验）")
		existing = nil
	}
	existingIDs := make(map[string]bool, len(existing))
	for _, a := range existing {
		existingIDs[a.ID] = true
	}

	// 滋生本动作的 result 存在集（triggered_by 同样要做幻影过滤——因果边指向
	// 不存在的节点是假溯源，比缺边更糟）。查失败退回纯格式校验。
	resultIDs := map[string]bool(nil)
	if results, rErr := graph.ListNodesByKind(ctx, taskID, core.KindResult); rErr == nil {
		resultIDs = make(map[string]bool, len(results))
		for _, r := range results {
			resultIDs[r.ID] = true
		}
	} else {
		i.logger.Warn().Err(rErr).
			Msg("查询 Result 失败，本轮跳过 triggered_by 存在性过滤（仅格式校验）")
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
		validTriggeredBy := filterValidDependencies(proposal.TriggeredBy, resultIDs)
		if len(validTriggeredBy) < len(proposal.TriggeredBy) {
			i.logger.Warn().Int("dropped", len(proposal.TriggeredBy)-len(validTriggeredBy)).
				Str("action_id", actionID).
				Msg("过滤掉无效/幻影的 triggered_by（图中不存在该 result——因果溯源只认真实节点）")
		}

		// 构建 Action 内容
		actionContent := map[string]interface{}{
			"type":         proposal.Type,
			"instruction":  proposal.Instruction,
			"complexity":   complexity,
			"priority":     priority,
			"reason":       proposal.Reason,
			"depends_on":   validDependsOn,
			"triggered_by": validTriggeredBy,
			"metadata":     proposal.Metadata,
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

// planningNarrative 把规划协议转成时间线人读叙述：结论 + 动作清单 + 理由。
// 协议原文随 Round.Raw 折叠可查——叙述只在此处发生一次，前端不做协议解释。
func planningNarrative(r PlanningResponse) string {
	var b strings.Builder
	if r.ShouldContinue {
		fmt.Fprintf(&b, "继续执行，本轮派发 %d 个动作：", len(r.Actions))
		for i, a := range r.Actions {
			if a.Type != "" {
				fmt.Fprintf(&b, "\n%d. [%s] %s", i+1, a.Type, a.Instruction)
			} else {
				fmt.Fprintf(&b, "\n%d. %s", i+1, a.Instruction)
			}
		}
	} else {
		b.WriteString("任务收敛，不再派发动作。")
	}
	if r.Reasoning != "" {
		b.WriteString("\n\n理由：" + r.Reasoning)
	}
	return b.String()
}
