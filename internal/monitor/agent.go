// Package monitor 实现独立的监察 Agent（MonitorAgent）
//
// 使用 ReActRuntime 进行 LLM 评估
package monitor

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/monitor/metrics"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
)

// Agent 是独立的监察 Agent。
type Agent struct {
	thoughts     reasoning.Tap // 思考流出口（nil=不流式）
	taskID       string
	agentRunID   string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	graph        *explorationgraph.Store
	eventBus     bus.EventBus
	router       *llm.Router // 每次评估现取 provider——配置/密钥变更经 30s TTL 对本任务生效
	complexity   llm.Complexity
	reactRuntime runtime.ReActRuntime
	interval     time.Duration
	logger       zerolog.Logger

	charter string // 角色章程（生效值；空=ReAct.Run 报错）
	maxIt   int    // ReAct 迭代上限（0=不设限）
	// contextRatio 是上下文历史预算占 provider.context_window 的比例（0=压缩器内置兜底）。
	contextRatio        float64
	distiller           runtime.Distiller // 上下文压缩纪要蒸馏器（nil=压缩降级纯截断）
	toolOutputTailBytes int               // 工具输出全局尾部钳制字节（0=不钳）

	// 时间序列指标收集器
	metricsCollector *metrics.Collector
}

// Config 是 Monitor Agent 的配置。
type Config struct {
	TaskID     string
	AgentRunID string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	Graph      *explorationgraph.Store
	EventBus   bus.EventBus
	Router     *llm.Router   // provider 每次评估经 Router.For 现取（与 planner/executor/evaluator 同口径）
	Complexity string        // LLM 档位（agent.complexity，simple|medium|complex）
	Interval   time.Duration // 评估间隔，默认 6 分钟
	Logger     zerolog.Logger

	FunctionTools []string // function_tools 白名单（agent 配置；nil=拒绝，[]=空集，["*"]=全量）

	// ContextRatio 是上下文历史预算占 provider.context_window 的比例（系统配置
	// compaction 组，任务开始时快照；0=压缩器内置兜底预算）。
	ContextRatio float64

	// Distiller 是上下文压缩纪要蒸馏器（nil=压缩降级纯截断）。
	Distiller runtime.Distiller

	// ToolOutputTailBytes 是工具输出进上下文前的全局尾部钳制字节（0=不钳）。
	ToolOutputTailBytes int

	// Activity 返回 action 距最近一次工具活动的时长（可选）。提供后
	// get_global_state 会向决策者暴露 idle_minutes——kill 判据从
	// "运行时长"升级为"活动停摆时长"，避免冤杀长时深挖的活跃 action。
	Activity func(actionID string) (time.Duration, bool)

	// WrapTool 把监察工具包进共享拦截链（遥测/心跳/时间线）——系统里
	// 破坏性最大的决策（kill）不应是审计盲区（实测 tool_invocation 0 行）。
	// nil=不包装（独立部署/测试）。
	WrapTool func(registry.Tool) registry.Tool

	Charter       string        // 角色章程（出厂值叠加用户自定义后的生效值）
	MaxIterations int           // ReAct 迭代上限（agent.max_iterations；0=不设限）
	Thoughts      reasoning.Tap // 思考流出口（认知循环注入；nil=不流式）
}

// New 创建 Monitor Agent 实例。
func New(cfg Config) *Agent {
	interval := cfg.Interval
	if interval == 0 {
		interval = constants.MonitorInterval
	}

	// 创建 ReAct 运行时
	reactRuntime := runtime.NewReActRuntime()

	// 创建指标收集器
	metricsCollector := metrics.NewCollector(cfg.Graph, cfg.TaskID, cfg.Logger)

	// 注册监察工具（function_tools 白名单过滤；nil=拒绝，[]=空集，["*"]=全量）
	var registryTools []registry.Tool
	if registry.Allows(cfg.FunctionTools, "get_global_state") {
		tool := NewGetGlobalStateTool(cfg.Graph, cfg.TaskID, metricsCollector)
		tool.activity = cfg.Activity
		registryTools = append(registryTools, tool)
	}
	if registry.Allows(cfg.FunctionTools, "publish_decision") {
		registryTools = append(registryTools, NewPublishDecisionTool(cfg.Graph, cfg.EventBus, cfg.TaskID))
	}
	for _, tool := range registryTools {
		if cfg.WrapTool != nil {
			tool = cfg.WrapTool(tool)
		}
		if err := reactRuntime.RegisterTool(tool); err != nil {
			cfg.Logger.Warn().Err(err).Str("tool", tool.Name()).Msg("注册工具失败")
		}
	}

	return &Agent{
		taskID:              cfg.TaskID,
		agentRunID:          cfg.AgentRunID,
		graph:               cfg.Graph,
		eventBus:            cfg.EventBus,
		router:              cfg.Router,
		complexity:          llm.ParseComplexity(cfg.Complexity),
		reactRuntime:        reactRuntime,
		interval:            interval,
		logger:              cfg.Logger,
		charter:             cfg.Charter,
		maxIt:               cfg.MaxIterations,
		contextRatio:        cfg.ContextRatio,
		distiller:           cfg.Distiller,
		toolOutputTailBytes: cfg.ToolOutputTailBytes,
		metricsCollector:    metricsCollector,
		thoughts:            cfg.Thoughts,
	}
}

// contextBudget 解析历史 token 预算：比例 × 本角色路由 provider 的 context_window。
// 比例未配置/读取失败返回 0——压缩器内置兜底预算接管（配置问题绝不阻断监察）。
func (a *Agent) contextBudget(ctx context.Context) int {
	if a.contextRatio <= 0 {
		return 0
	}
	cw, err := a.router.ContextWindow(ctx, a.complexity)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取 context_window 失败，压缩预算用内置兜底")
		return 0
	}
	return int(float64(cw) * a.contextRatio)
}

// hasSupervisableWork 判断图上是否有可监察对象（open/running 动作）。
// 判定失败保守返回 true（宁可空评一次，不可漏评真实在途）。
func (a *Agent) hasSupervisableWork(ctx context.Context) bool {
	states, err := a.graph.ListActionStates(ctx, a.taskID)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取动作状态失败，保守视为有在途工作")
		return true
	}
	for _, st := range states {
		if st == explorationgraph.StateOpen || st == explorationgraph.StateRunning {
			return true
		}
	}
	return false
}

// Run 周期评估主循环（ctx 取消即停止）。
func (a *Agent) Run(ctx context.Context) error {
	// LLM 审计维度：监察调用归 task/本轮 run、角色 monitor。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: a.taskID, AgentRunID: a.agentRunID, Role: "monitor"})

	a.logger.Info().Dur("interval", a.interval).Msg("monitor agent starting")

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	// 启动即评估仅在有在途工作时成立：空图上的首评只会得出「无可监察」
	// 的确定结论——烧一轮 LLM（含强制总结共 3 次调用）监察不存在的工作。
	// 图上无 open/running 动作时跳过首评，等定时器（届时若任务真实必有在途）。
	if a.hasSupervisableWork(ctx) {
		if err := a.evaluationFailure(a.evaluate(ctx), "首次评估"); err != nil {
			return err
		}
	} else {
		a.logger.Info().Msg("启动时图上无在途动作，跳过首评（无可监察对象）")
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Msg("monitor agent stopped")
			return ctx.Err()

		case <-ticker.C:
			// 同一守卫作用于周期评估：静止图（全终态、无在途）无监察对象，
			// 规划器自会对静态图反应——monitor 的价值在动作执行中的纠偏。
			if !a.hasSupervisableWork(ctx) {
				a.logger.Debug().Msg("周期评估跳过：图上无在途动作")
				continue
			}
			if err := a.evaluationFailure(a.evaluate(ctx), "周期评估"); err != nil {
				return err
			}
		}
	}
}

// evaluationFailure 处置一次评估失败：LLM 配置错误原样返回（致命，Run 退出）；
// 其余失败记录后继续——监察是辅助面，单次评估失败不应拖垮任务。
func (a *Agent) evaluationFailure(err error, phase string) error {
	if err == nil {
		return nil
	}
	if llm.IsConfigError(err) {
		a.logger.Error().Err(err).Str("phase", phase).Msg("LLM 配置错误，监察无法继续")
		return err
	}
	a.logger.Error().Err(err).Str("phase", phase).Msg("评估失败（继续运行）")
	return nil
}

// evaluate 执行一次全局评估（使用 ReActRuntime）
func (a *Agent) evaluate(ctx context.Context) error {
	a.logger.Info().Msg("starting global evaluation with ReActRuntime")

	// 更新时间序列指标
	if err := a.metricsCollector.Update(ctx); err != nil {
		a.logger.Warn().Err(err).Msg("更新指标失败，继续评估")
	}

	// 构建评估目标
	objective := a.buildEvaluationObjective()

	// 构建系统提示
	systemPrompt := a.buildSystemPrompt()

	// provider 每次评估现取：agent.complexity 档位经 Router 解析，配置/密钥
	// 变更经 30s TTL 对运行中任务生效（与 planner/executor/evaluator 同口径）。
	provider, err := a.router.For(ctx, a.complexity)
	if err != nil {
		return fmt.Errorf("monitor: 获取 provider: %w", err)
	}

	// 配置 ReAct 运行
	config := &runtime.ReActConfig{
		Objective:            objective,
		SystemPrompt:         systemPrompt,
		LLMProvider:          provider,
		MaxIterations:        a.maxIt, // 迭代上限 = agent.max_iterations（配置即事实；0=不设限）
		Temperature:          0.3,     // 较低温度，确保稳定性
		MaxTokens:            4000,
		MessageModifierChain: runtime.NewCompactionChain(a.contextBudget(ctx), "monitor", a.distiller),
		ToolOutputTailBytes:  a.toolOutputTailBytes,
		// 监察链路也会多轮调工具（get_global_state 深挖/重试）：循环检测与
		// 无进展止损与其余三个 agent 同口径；GoalAlignment 不适用（评估目标
		// 每轮现构建，无固定目标可对齐——与 evaluator judge 同理）。
		MiddlewareChain: runtime.NewMiddlewareChain(
			runtime.NewLoopDetectionMiddleware(6, 5), // 连续 6 次相同工具 / 5 次相似思考
			runtime.NewProgressTrackingMiddleware(8), // 8 轮无新输出终止
		),

		OnIteration: func(iteration int, status runtime.IterationStatus) {
			a.logger.Info().
				Int("iteration", iteration).
				Str("status", string(status)).
				Msg("[MONITOR] ReAct 迭代")
		},
	}

	// 运行 ReAct 循环（思考流出口非空时逐段回调增量）
	if a.thoughts != nil {
		config.OnThoughtDelta = a.thoughts.Delta
		config.OnThinkingDelta = a.thoughts.ThinkingDelta
	}
	result, err := a.reactRuntime.Run(ctx, config)
	if err != nil {
		return fmt.Errorf("ReAct runtime execution: %w", err)
	}
	if a.thoughts != nil {
		a.thoughts.Done(reasoning.RoundOf(result))
	}

	a.logger.Info().
		Str("status", string(result.Status)).
		Int("iterations", result.Iterations).
		Msg("evaluation completed")

	return nil
}

// buildEvaluationObjective 构建评估目标
func (a *Agent) buildEvaluationObjective() string {
	return fmt.Sprintf(`你是任务 %s 的监察 Agent。

你的职责：
1. 检查任务的全局状态
2. 识别停滞、低效或异常的 Actions
3. 做出监察决策（kill_action 或 request_replan）

请使用以下工具：
- get_global_state：获取任务的全局状态
- publish_decision：发布监察决策

评估标准：
- kill_action 只看活动停摆：running_actions[].idle_minutes 超过 25 分钟（远超单次长 LLM 生成）才果断 kill——idle_minutes=-1 表示还没开始干活，超过 10 分钟无活动同样可 kill；running_minutes 长但 idle_minutes 小的动作正在正常深挖，绝不可 kill
- 大量 failed Actions → 考虑 replan
- 缺乏进展 → 考虑 replan

请开始评估。`, a.taskID)
}

// buildSystemPrompt 组装监察 system prompt：即章程本身。监察的决策经
// publish_decision 工具调用发布，形状由工具 schema 约束，最终文本不被解析，
// 因此没有需要随代码固定的输出契约。章程为空时返回空串让 ReAct.Run 报错
// （自定义层在写入时已拒绝空章程，此处只是兜底）。
func (a *Agent) buildSystemPrompt() string {
	charter := strings.TrimSpace(a.charter)
	if charter == "" {
		return ""
	}
	return charter
}

// ============================================
// 辅助类型（保留用于工具内部）
// ============================================

// GlobalState 是任务的全局状态快照。
type GlobalState struct {
	Objective explorationgraph.ObjectiveNode `json:"objective"`
	Actions   []explorationgraph.Node        `json:"actions"`
	// Results 是坐实发现（图上 kind=result 节点）。与 finding 表（安全域
	// 交付物）是两个概念：result 是图状态面，finding 是交付面。
	Results []explorationgraph.Node `json:"results"`

	// RunningActions 是 running 态动作的已运行时长视图——monitor 的 kill 阈值
	// 判断依据（e2e 实测：只给原始 created_at/updated_at 时间戳时，LLM 无从
	// 计算"已运行多少分钟"，20 分钟 kill 职责形同虚设）。决策变量必须显式喂给
	// 决策者，不能指望它做时间戳算术。
	RunningActions []RunningActionView `json:"running_actions"`

	// Metrics 是时间窗口指标（P5）
	// 用于判断"每小时 <3 actions"、"超过 1 小时无新 Result"等场景
	Metrics metrics.Snapshot `json:"metrics"`
}

// RunningActionView 是 running 动作的监察视图。
type RunningActionView struct {
	ID             string  `json:"id"`
	Instruction    string  `json:"instruction"`
	RunningMinutes float64 `json:"running_minutes"`
	// IdleMinutes 是距最近一次工具活动的分钟数；-1 = 尚无任何工具活动
	// （还没开始干活），与"干着干着停了"是两种完全不同的处置依据。
	IdleMinutes float64 `json:"idle_minutes"`
}

// Decision 是监察决策。
type Decision struct {
	Type     string `json:"type"`                // "kill_action" | "request_replan"
	ActionID string `json:"action_id,omitempty"` // kill_action 需要
	Reason   string `json:"reason"`              // 决策理由
}
