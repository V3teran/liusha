// Package planner 实现基于 LLM 的智能规划引擎
package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	plannercontract "github.com/V3teran/liusha/internal/planner/contract"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/scenario"
)

// ErrPlanOutputParse 表示规划输出经修复层后仍不可解析。它区别于网络类瞬时
// 错误：模型换一次生成极可能产出合法 JSON，因此规划 Agent 对它的重试策略是
// 「再问一次」而非放弃本批规划。
var ErrPlanOutputParse = errors.New("规划输出不可解析")

// Engine 是基于 LLM 的智能规划器
type Engine struct {
	router     *llm.Router
	logger     zerolog.Logger
	charter    string         // 角色章程（生效值：出厂叠加用户自定义）
	scenario   string         // 任务场景块（scenario.Render；空 = 无场景）
	complexity llm.Complexity // LLM 档位（agent 生效配置的 complexity）
	thoughts   reasoning.Tap  // 思考流出口（认知循环注入；nil=不流式）
	maxIt      int            // ReAct 迭代上限（agent.max_iterations；0=不设限）
	// contextRatio 是上下文历史预算占 provider.context_window 的比例（系统配置
	// compaction 组，任务开始时快照；0=压缩器内置兜底预算）。
	contextRatio        float64
	distiller           runtime.Distiller // 上下文压缩纪要蒸馏器（nil=压缩降级纯截断）
	toolOutputTailBytes int               // 工具输出全局尾部钳制字节（0=不钳）
	timeout             time.Duration     // 单次规划超时基准（0=不设限；默认 3 分钟）
	budget              time.Duration     // 当前生效的规划预算：随连续 deadline 失败自适应放大，成功后回落
	roster              []AgentBrief      // 四 Agent 名册（name + 调度摘要；空=规划 prompt 不带该节）

	// extraTools 是白名单 function 工具（search_corpus 等，装配层按 agent 白名单从共享
	// 工厂构建并已包装进共享拦截链），Plan 时按白名单注册进 ReAct，不再经 toolWrap。
	extraTools []registry.Tool

	// toolWrap 是共享拦截链包装器（cognition 注入 reg.WrapTool；nil=不包装），只包装
	// 引擎自有的图读工具——它们与 executor 工具同一执行面，遥测/时间线无盲区。
	toolWrap func(registry.Tool) registry.Tool
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
func (e *Engine) WithAgentRoster(roster []AgentBrief) *Engine {
	e.roster = roster
	return e
}

// maxBudgetFactor 是自适应预算的放大上限（基准的倍数）——无界放大等于取消保护。
const maxBudgetFactor = 3

// NewEngine 创建智能规划器
func NewEngine(router *llm.Router, logger zerolog.Logger) *Engine {
	return &Engine{
		router:  router,
		logger:  logger.With().Str("component", "planner_intelligence").Logger(),
		timeout: 3 * time.Minute, // 默认单次规划超时 3 分钟
		budget:  3 * time.Minute,
	}
}

// WithCharter 注入角色章程（出厂值叠加用户自定义后的生效值）。
func (e *Engine) WithCharter(charter string) *Engine {
	e.charter = charter
	return e
}

// WithScenario 注入任务场景块（scenario.Render；空 = 无场景），拼在常规规划与结果分析两份
// 系统提示词末尾。
func (e *Engine) WithScenario(block string) *Engine {
	e.scenario = block
	return e
}

// WithMaxIterations 注入 ReAct 迭代上限（agent.max_iterations；0=不设限）。
func (e *Engine) WithMaxIterations(n int) *Engine {
	e.maxIt = n
	return e
}

// WithContextRatio 注入上下文历史预算占比（系统配置 compaction 组；0=压缩器内置兜底）。
func (e *Engine) WithContextRatio(ratio float64) *Engine {
	e.contextRatio = ratio
	return e
}

// WithDistiller 注入上下文压缩纪要蒸馏器（nil=压缩降级纯截断）。
func (e *Engine) WithDistiller(d runtime.Distiller) *Engine {
	e.distiller = d
	return e
}

// WithToolOutputTailBytes 注入工具输出全局尾部钳制字节（0=不钳）。
func (e *Engine) WithToolOutputTailBytes(n int) *Engine {
	e.toolOutputTailBytes = n
	return e
}

// WithExtraTools 注入白名单 function 工具（装配层按 agent 白名单从共享工厂
// 构建；Plan 时经 registry.Allows 复核后注册进 ReAct）。
func (e *Engine) WithExtraTools(ts []registry.Tool) *Engine {
	e.extraTools = ts
	return e
}

// WithComplexity 注入 LLM 档位（agent 生效配置的 complexity；
// 空回退 medium）。
func (e *Engine) WithComplexity(complexity string) *Engine {
	e.complexity = llm.ParseComplexity(complexity)
	return e
}

// contextBudget 解析历史 token 预算：比例 × 本角色路由 provider 的 context_window。
// 比例未配置/读取失败返回 0——压缩器内置兜底预算接管（配置问题绝不阻断规划）。
func (e *Engine) contextBudget(ctx context.Context) int {
	if e.contextRatio <= 0 {
		return 0
	}
	cw, err := e.router.ContextWindow(ctx, e.complexity)
	if err != nil {
		e.logger.Warn().Err(err).Msg("读取 context_window 失败，压缩预算用内置兜底")
		return 0
	}
	return int(float64(cw) * e.contextRatio)
}

// WithThoughts 注入思考流出口（认知循环的会话流实现；nil=不流式）。
func (e *Engine) WithThoughts(tap reasoning.Tap) *Engine {
	e.thoughts = tap
	return e
}

// WithToolWrapper 注入共享拦截链包装器（cognition 传 reg.WrapTool；nil=不包装）。
func (e *Engine) WithToolWrapper(fn func(registry.Tool) registry.Tool) *Engine {
	e.toolWrap = fn
	return e
}

// WithTimeout 注入单次规划超时（0=不设限；默认 3 分钟）。
func (e *Engine) WithTimeout(timeout time.Duration) *Engine {
	e.timeout = timeout
	return e
}

// RefutedHypothesis 是被复现门出圈的假设：反驳原因区分"证据没拿到或构不成证据"
// （not_reproducible/invalid_recipe/assert_not_discriminative——换配方或判据重试）、
// "证据到了但主张不成立"（claim_invalid——方向作废）与"评估没跑完"
// （judge_unavailable——主张未被检验，原方向重提），处置写在规划者章程里。
type RefutedHypothesis struct {
	Statement string
	Reason    string
	Detail    string // 评估者证伪时的理由（缺了什么证据、哪部分已被证明）；机器判出的证伪为空
}

// Fact 是已确认的世界状态断言（事实观察）：会话已建立、某机制由什么控制、
// 资源是否存在——规划的基底，直接消费、无需验证。Category 是它的类别，
// 规划时按类别分组呈现。
type Fact struct {
	Category  explorationgraph.ObservationCategory
	Statement string
}

// PlanningContext 规划上下文
type PlanningContext struct {
	Objective         string                  // 根目标描述（任务目标）
	Objectives        []explorationgraph.Node // 全部目标（根目标 + 派生目标，含已收敛的；呈现时只列进行中的派生目标）
	CompletedActions  []explorationgraph.Node // 已完成的 Action
	PendingActions    []explorationgraph.Node // 待执行的 Action
	Results           []explorationgraph.Node // 已确认的结果（经判定坐实的主张）
	FailedActions     []explorationgraph.Node // 失败的 Action
	AbortedActions    []explorationgraph.Node // 已中止的 Action（任务收口或目标调整时未执行完；无对错判断，相关的可重提）
	RejectedActions   []explorationgraph.Node // 已否决的 Action（监察者或用户判定不该做；含原因与否决方，禁止等效重提）
	RefutedHypotheses []RefutedHypothesis     // 已被复现门证伪的假设（含反驳原因，防止复读）
	PendingHypotheses []string                // 待验证的假设陈述（复现门验证中，不必为它们安排动作）
	Facts             []Fact                  // 已确认的世界状态（规划基底，直接消费）
	ReplanReasons     []string                // 强制重规划的理由（常规规划为空）
}

// ActionProposal LLM 返回的 Action 提案
type ActionProposal struct {
	Instruction string   `json:"instruction"`  // 执行指令
	Complexity  string   `json:"complexity"`   // 复杂度: simple/moderate/complex
	Priority    string   `json:"priority"`     // 优先级: critical/high/medium/low
	Reasoning   string   `json:"reasoning"`    // 规划理由
	TriggeredBy []string `json:"triggered_by"` // 滋生本动作的 Result ID（发现→动作因果溯源；可选，仅在真实成立时声明）
}

// PlanningResponse LLM 响应
type PlanningResponse struct {
	ShouldContinue bool             `json:"should_continue"` // 是否应该继续执行
	Reasoning      string           `json:"reasoning"`       // 推理过程
	Actions        []ActionProposal `json:"actions"`         // 新提议的 Action
}

// PlanOutcome 是一次常规规划的结论。
type PlanOutcome struct {
	// Stop 为真表示规划者判定停止探索（契约的 should_continue=false）：这是对任务的结论，
	// 不是「本轮没有新动作」——由 Agent 收回规划者尚未做完的动作，等其余工作落定后收敛。
	Stop    bool
	Actions []explorationgraph.ActionDraft // 新提议的动作（Stop 时为空）
}

// planTools 按 function_tools 白名单列出本次规划要注册的工具。共享拦截链只包装
// 引擎自有的图读工具：额外工具由装配层包装后注入，再包一层会让每次调用在审计链
// 里落两条记录、在时间线上推两次。
func (e *Engine) planTools(graph *explorationgraph.Store, functionTools []string) []registry.Tool {
	var out []registry.Tool
	for _, tool := range []registry.Tool{NewObserveStateTool(graph), NewEvaluateProgressTool(graph)} {
		if !registry.Allows(functionTools, tool.Name()) {
			continue
		}
		if e.toolWrap != nil {
			tool = e.toolWrap(tool)
		}
		out = append(out, tool)
	}
	for _, tool := range e.extraTools {
		if registry.Allows(functionTools, tool.Name()) {
			out = append(out, tool)
		}
	}
	return out
}

// PlanRequest 是一次规划的入参。
type PlanRequest struct {
	FunctionTools []string // function_tools 白名单（nil=拒绝，[]=空集，["*"]=全量）
	ReplanReasons []string // 强制重规划的理由（常规规划为空）
}

// Plan 基于当前探索图状态生成新的 Action——ReAct 形态：
// LLM 经 observe_state/evaluate_progress 工具自主观察探索图，思考后输出规划 JSON。
// 落图仍由 agent.go 单点执行（转换 + 依赖过滤 + 事件发布），ReAct 只负责决策。
func (e *Engine) Plan(ctx context.Context, graph *explorationgraph.Store, taskID string, req PlanRequest) (PlanOutcome, error) {
	functionTools := req.FunctionTools
	e.logger.Info().Msg("开始智能规划")

	// 超时保护（防止 LLM 调用无限挂起）+ 自适应预算：deadline 型失败（LLM 慢速期
	// 预算耗尽）用同一预算重试是确定性失败——预算随连续超时翻倍、封顶基准
	// maxBudgetFactor 倍，任一次成功即回落。调用方的重试阶梯只退避间隔，
	// 退避不了预算本身。
	if e.budget > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, e.budget)
		defer cancel()
		e.logger.Debug().
			Str("timeout", e.budget.String()).
			Msg("规划超时保护已启用")
	}

	// 1. 收集规划上下文（作为首条输入的紧凑摘要；细节靠工具按需深挖）
	planCtx, err := e.gatherContext(ctx, graph, taskID)
	if err != nil {
		return PlanOutcome{}, fmt.Errorf("收集规划上下文失败: %w", err)
	}
	planCtx.ReplanReasons = req.ReplanReasons
	prompt := e.buildPlanningPrompt(planCtx)

	// 2. 组装 ReAct 规划器：observe_state / evaluate_progress 只读图工具
	provider, err := e.router.For(ctx, e.complexity)
	if err != nil {
		return PlanOutcome{}, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	// function_tools 白名单过滤（nil=拒绝，[]=空集，["*"]=全量——严格白名单，与 cli_tools 语义一致）
	react := runtime.NewReActRuntime()
	for _, tool := range e.planTools(graph, functionTools) {
		if err := react.RegisterTool(tool); err != nil {
			return PlanOutcome{}, fmt.Errorf("注册 %s 失败: %w", tool.Name(), err)
		}
	}

	// 图工具经 ctx 取 task_id（类型化 key；工具侧用 taskIDFromContext 读取）
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
	if e.thoughts != nil {
		onThoughtDelta = e.thoughts.Delta
		onThinkingDelta = e.thoughts.ThinkingDelta
	}
	result, err := react.Run(ctx, &runtime.ReActConfig{
		Objective:                  prompt,
		SystemPrompt:               e.buildPlannerSystemPrompt(),
		LLMProvider:                provider,
		MaxIterations:              e.maxIt, // 迭代上限 = agent.max_iterations（配置即事实；0=不设限）
		MaxConsecutiveToolFailures: 8,       // 连续工具失败止损阈值（单机制：runtime 内置，随装配按角色调参）
		OnThoughtDelta:             onThoughtDelta,
		OnThinkingDelta:            onThinkingDelta,
		MessageModifierChain:       runtime.NewCompactionChain(e.contextBudget(ctx), "planner", e.distiller),
		ToolOutputTailBytes:        e.toolOutputTailBytes,
		MiddlewareChain:            middlewareChain,
		// Planner 的结构化输出格式
		OutputFormatPrompt: "你是规划者（Planner），需要输出下一批要执行的Action。",
		OutputFormatSchema: plannercontract.Text, // 强制总结时重申输出契约（唯一一份）
	})
	if err != nil {
		e.adaptBudget(err)
		return PlanOutcome{}, fmt.Errorf("ReAct 规划失败: %w", err)
	}
	e.budget = e.timeout // 成功即回落基准预算

	// 4. 解析最终答案为 PlanningResponse。终态叙述在解析边界完成：协议转人读
	//    摘要作正文（谁解析谁叙述，前端不解释协议；协议原文由审计面保存）；
	//    解析失败时正文回退原文——终态帧始终落库，清空气泡语义不受解析成败影响。
	response, perr := e.parsePlanningResponse(result.FinalAnswer)
	round := reasoning.RoundOf(result)
	if perr == nil && response != nil {
		round.Summary = planningNarrative(*response)
	}
	if e.thoughts != nil {
		e.thoughts.Done(round)
	}
	if perr != nil {
		return PlanOutcome{}, fmt.Errorf("解析规划输出失败: %w", perr)
	}

	// 5. 停止探索：结论原样交给 Agent 落定（契约要求此时 actions 为空，给了也不执行）
	if !response.ShouldContinue {
		e.logger.Info().
			Str("reasoning", response.Reasoning).
			Int("ignored_actions", len(response.Actions)).
			Msg("LLM 判定停止探索")
		return PlanOutcome{Stop: true}, nil
	}

	// 6. 转换 LLM 提案为动作草稿
	drafts := e.convertProposals(ctx, graph, taskID, response.Actions)
	e.logger.Info().Int("action_count", len(drafts)).Msg("智能规划完成")
	return PlanOutcome{Actions: drafts}, nil
}

// gatherContext 收集规划所需的上下文信息
func (e *Engine) gatherContext(ctx context.Context, graph *explorationgraph.Store, taskID string) (*PlanningContext, error) {
	planCtx := &PlanningContext{}

	// 全部目标（根目标 + 派生目标）读入规划上下文
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
		case explorationgraph.StateCompleted:
			planCtx.CompletedActions = append(planCtx.CompletedActions, action)
		case explorationgraph.StateOpen, explorationgraph.StateRunning:
			planCtx.PendingActions = append(planCtx.PendingActions, action)
		case explorationgraph.StateFailed:
			planCtx.FailedActions = append(planCtx.FailedActions, action)
		case explorationgraph.StateAborted:
			planCtx.AbortedActions = append(planCtx.AbortedActions, action)
		case explorationgraph.StateRejected:
			// 否决必须进规划视野：携带原因与否决方呈现「这一步不该做及为什么」，否则
			// 规划者失明、重规划会原样再派等效动作。
			planCtx.RejectedActions = append(planCtx.RejectedActions, action)
		}
	}

	// 获取已确认的结果
	results, err := graph.ListNodesByKind(ctx, taskID, core.KindResult)
	if err != nil {
		return nil, fmt.Errorf("获取 Result 失败: %w", err)
	}
	planCtx.Results = results

	// 观察一次取全量，事实（世界状态）与待验证、已证伪的假设共用——避免同表连查多次。
	observations, err := graph.ListNodesByKind(ctx, taskID, core.KindObservation)
	if err != nil {
		return nil, fmt.Errorf("获取 Observation 失败: %w", err)
	}
	for _, obs := range observations {
		var c struct {
			Category  explorationgraph.ObservationCategory `json:"category"`
			Statement string                               `json:"statement"`
		}
		if json.Unmarshal(obs.Content, &c) != nil || c.Statement == "" {
			continue
		}
		switch {
		case obs.IsFact():
			// 已确认的世界状态（写入即终态）——规划的基底：已建立的会话、
			// 已探明的机制、已确认的资源，直接拿来用。
			planCtx.Facts = append(planCtx.Facts, Fact{Category: c.Category, Statement: c.Statement})
		case obs.HasVerificationStatus(explorationgraph.VerificationPending):
			// 已上报、正在复现门里排队的主张：规划者看不见它们时会排动作重新取得，
			// 造成重复执行与重复上报。
			planCtx.PendingHypotheses = append(planCtx.PendingHypotheses, c.Statement)
		case obs.HasVerificationStatus(explorationgraph.VerificationRefuted):
			// 已证伪的假设——规划器对"哪条路已试死"的权威事实源，防同方向反复
			// 重提（复读机）。原因一并带出：复现失败类可换配方重试，语义证伪类
			// 才是方向作废，评估未完成可原方向重提。
			planCtx.RefutedHypotheses = append(planCtx.RefutedHypotheses, RefutedHypothesis{
				Statement: c.Statement,
				Reason:    obs.RefuteReason(),
				Detail:    obs.RefuteDetail(),
			})
		}
	}

	return planCtx, nil
}

// buildPlanningPrompt 构建规划 prompt
func (e *Engine) buildPlanningPrompt(ctx *PlanningContext) string {
	var sb strings.Builder

	// 1. 多智能体分工名册（各角色调度摘要，agent.description 前端可编辑）
	sb.WriteString(buildTeamRosterSection(e.roster))

	// 3. 任务目标
	sb.WriteString(buildObjectiveSection(ctx.Objective, ctx.Objectives))

	// 3.2 重规划理由（常规规划不渲染）
	sb.WriteString(buildReplanSection(ctx.ReplanReasons))

	// 4. 统计信息
	sb.WriteString(buildStatisticsSection(ctx))

	// 5. 已完成的工作
	sb.WriteString(buildCompletedActionsSection(ctx.CompletedActions))

	// 6. 进行中的工作
	sb.WriteString(buildPendingActionsSection(ctx.PendingActions))

	// 7. 失败的尝试
	sb.WriteString(buildFailedActionsSection(ctx.FailedActions))

	// 7.5 已否决的动作与已中止的动作
	sb.WriteString(buildRejectedActionsSection(ctx.RejectedActions))
	sb.WriteString(buildAbortedActionsSection(ctx.AbortedActions))

	// 8. 已坐实的结果
	sb.WriteString(buildResultsSection(ctx.Results))

	// 8.5 已确认的事实（规划基底）
	sb.WriteString(buildFactsSection(ctx.Facts))

	// 9. 待验证与已被证伪的假设
	sb.WriteString(buildPendingHypothesesSection(ctx.PendingHypotheses))
	sb.WriteString(buildRefutedHypothesesSection(ctx.RefutedHypotheses))

	// 11. 输出提醒（格式以 system prompt 的输出契约为唯一来源）
	sb.WriteString("按系统提示中的「输出契约」只输出一个 JSON 对象。\n")

	return sb.String()
}

// buildPlannerSystemPrompt 组装规划 system prompt：章程（可自定义）+ 输出契约
// （planner/contract，与 parsePlanningResponse 同改同测，不可自定义）+ 任务场景块（无场景时
// 没有）。章程为空时返回空串，
// 让 ReAct.Run 报错——自定义层在写入时已拒绝空章程，此处只是兜底。
func (e *Engine) buildPlannerSystemPrompt() string {
	charter := strings.TrimSpace(e.charter)
	if charter == "" {
		return ""
	}
	return scenario.Append(charter+"\n\n"+plannercontract.Text, e.scenario)
}

// parsePlanningResponse 从 ReAct 最终答案解析规划 JSON。
func (e *Engine) parsePlanningResponse(content string) (*PlanningResponse, error) {
	if content == "" {
		return nil, fmt.Errorf("规划输出为空")
	}

	var response PlanningResponse
	if err := llm.ParseJSON(content, &response); err != nil {
		e.logger.Error().
			Err(err).
			Str("llm_response", content).
			Msg("解析规划输出失败")
		return nil, fmt.Errorf("%w: %w", ErrPlanOutputParse, err)
	}

	return &response, nil
}

// adaptBudget 在 deadline 型失败后放大下一轮规划预算（翻倍，封顶基准
// maxBudgetFactor 倍）；非 deadline 失败不动预算。Engine 由单个
// planner goroutine 串行使用，无并发竞争。
func (e *Engine) adaptBudget(err error) {
	if !errors.Is(err, context.DeadlineExceeded) || e.timeout <= 0 {
		return
	}
	ceiling := e.timeout * maxBudgetFactor
	if e.budget < ceiling {
		e.budget *= 2
		if e.budget > ceiling {
			e.budget = ceiling
		}
	}
	e.logger.Warn().
		Str("budget", e.budget.String()).
		Msg("规划预算耗尽（LLM 慢速期），下一轮预算已放大")
}

// filterKnownIDs 过滤 LLM 引用的节点 ID：非 UUID（如 "0"）与图中不存在的 ID（多为
// 照抄 prompt 示例）都剔除——指向不存在节点的溯源边是假溯源。known 为 nil 时只做
// 格式校验。
func filterKnownIDs(ids []string, known map[string]bool) []string {
	valid := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, err := uuid.Parse(id); err != nil {
			continue
		}
		if known != nil && !known[id] {
			continue
		}
		valid = append(valid, id)
	}
	return valid
}

// convertProposals 把 LLM 提案转成动作草稿：优先级与复杂度进节点层，因果引用随草稿
// 交给落图（落成 triggers 边），内容只留动作本身——类型、指令与规划理由。
func (e *Engine) convertProposals(ctx context.Context, graph *explorationgraph.Store, taskID string, proposals []ActionProposal) []explorationgraph.ActionDraft {
	// 滋生本动作的 result 存在集（triggered_by 同样要做幻影过滤——因果边指向
	// 不存在的节点是假溯源，比缺边更糟）。查失败退回纯格式校验。
	resultIDs := map[string]bool(nil)
	if results, rErr := graph.ListNodesByKind(ctx, taskID, core.KindResult); rErr == nil {
		resultIDs = make(map[string]bool, len(results))
		for _, r := range results {
			resultIDs[r.ID] = true
		}
	} else {
		e.logger.Warn().Err(rErr).
			Msg("查询 Result 失败，本轮跳过 triggered_by 存在性过滤（仅格式校验）")
	}

	var drafts []explorationgraph.ActionDraft
	for _, proposal := range proposals {
		// 生成节点 ID
		actionID := uuid.New().String()

		complexity := explorationgraph.NormalizeComplexity(proposal.Complexity)
		priority := explorationgraph.NormalizePriority(proposal.Priority)

		validTriggeredBy := filterKnownIDs(proposal.TriggeredBy, resultIDs)
		if len(validTriggeredBy) < len(proposal.TriggeredBy) {
			e.logger.Warn().Int("dropped", len(proposal.TriggeredBy)-len(validTriggeredBy)).
				Str("action_id", actionID).
				Msg("过滤掉无效/幻影的 triggered_by（图中不存在该 result——因果溯源只认真实节点）")
		}

		contentBytes, _ := json.Marshal(map[string]string{
			"instruction": proposal.Instruction,
			"reasoning":   proposal.Reasoning,
		})

		openState := explorationgraph.StateOpen
		node := explorationgraph.Node{
			ID:         actionID,
			TaskID:     taskID,
			Kind:       core.KindAction,
			Content:    contentBytes,
			State:      &openState,
			Priority:   priority,
			Complexity: &complexity,
			SourceType: explorationgraph.SourcePlanner,
			CreatedAt:  time.Now(),
		}

		drafts = append(drafts, explorationgraph.ActionDraft{Node: node, TriggeredBy: validTriggeredBy})

		e.logger.Debug().
			Str("action_id", actionID).
			Str("priority", string(priority)).
			Msg("生成 Action 节点")
	}

	return drafts
}

// planningNarrative 把规划协议转成时间线人读叙述：结论 + 动作清单 + 理由。
// 协议原文随 Round.Raw 折叠可查——叙述只在此处发生一次，前端不做协议解释。
func planningNarrative(r PlanningResponse) string {
	var b strings.Builder
	if r.ShouldContinue {
		fmt.Fprintf(&b, "继续执行，本轮派发 %d 个动作：", len(r.Actions))
		for i, a := range r.Actions {
			fmt.Fprintf(&b, "\n%d. %s", i+1, a.Instruction)
		}
	} else {
		b.WriteString("任务收敛，不再派发动作。")
	}
	if r.Reasoning != "" {
		b.WriteString("\n\n理由：" + r.Reasoning)
	}
	return b.String()
}
