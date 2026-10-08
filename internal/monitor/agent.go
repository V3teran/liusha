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
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/registry"
)

// Agent 是独立的监察 Agent。
type Agent struct {
	taskID       string
	agentRunID   string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	graph        *explorationgraph.Store
	eventBus     bus.Bus
	provider     llm.Provider
	reactRuntime runtime.ReActRuntime
	interval     time.Duration
	logger       zerolog.Logger

	systemPrompt string // 角色章程（agent.system_prompt；空=不渲染）
	maxIt        int    // ReAct 迭代上限（0=不设限）

	// Checkpoint 系统
	checkpointer     core.Checkpointer
	checkpointPolicy runtime.CheckpointPolicy
}

// Config 是 Monitor Agent 的配置。
type Config struct {
	TaskID     string
	AgentRunID string // 本轮认知循环的 agent_run.id（LLM 审计归属）
	Graph      *explorationgraph.Store
	EventBus   bus.Bus
	Provider   llm.Provider
	Interval   time.Duration // 评估间隔，默认 6 分钟
	Logger     zerolog.Logger

	FunctionTools []string // function_tools 白名单（agent 配置；nil=全量，空=空集）

	SystemPrompt  string // 角色章程（agent.system_prompt，运维经前端可调；空=不渲染）
	MaxIterations int    // ReAct 迭代上限（agent.max_iterations；0=不设限）

	// Checkpoint 配置（可选）
	Checkpointer     core.Checkpointer        // nil 表示禁用 checkpoint
	CheckpointPolicy runtime.CheckpointPolicy // nil 使用默认策略
}

// New 创建 Monitor Agent 实例。
func New(cfg Config) *Agent {
	interval := cfg.Interval
	if interval == 0 {
		interval = constants.MonitorInterval
	}

	// 创建 ReAct 运行时
	reactRuntime := runtime.NewReActRuntime()

	// 注册监察工具（function_tools 白名单过滤；nil=全量，空=空集）
	var registryTools []registry.Tool
	if registry.Allows(cfg.FunctionTools, "get_global_state") {
		registryTools = append(registryTools, NewGetGlobalStateTool(cfg.Graph, cfg.TaskID))
	}
	if registry.Allows(cfg.FunctionTools, "publish_decision") {
		registryTools = append(registryTools, NewPublishDecisionTool(cfg.Graph, cfg.EventBus, cfg.TaskID))
	}
	for _, tool := range registryTools {
		if err := reactRuntime.RegisterTool(tool); err != nil {
			cfg.Logger.Warn().Err(err).Str("tool", tool.Name()).Msg("注册工具失败")
		}
	}

	// 默认 Checkpoint 策略：每 3 次迭代保存（Monitor 迭代少）
	checkpointPolicy := cfg.CheckpointPolicy
	if checkpointPolicy == nil && cfg.Checkpointer != nil {
		checkpointPolicy = runtime.NewIterationCheckpointPolicy(3)
	}

	return &Agent{
		taskID:           cfg.TaskID,
		agentRunID:       cfg.AgentRunID,
		graph:            cfg.Graph,
		eventBus:         cfg.EventBus,
		provider:         cfg.Provider,
		reactRuntime:     reactRuntime,
		interval:         interval,
		logger:           cfg.Logger,
		systemPrompt:     cfg.SystemPrompt,
		maxIt:            cfg.MaxIterations,
		checkpointer:     cfg.Checkpointer,
		checkpointPolicy: checkpointPolicy,
	}
}

// Name 返回 Agent 的名称。

// Run 周期评估主循环（ctx 取消即停止）。
func (a *Agent) Run(ctx context.Context) error {
	// LLM 审计维度：监察调用归 task/本轮 run、角色 monitor。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: a.taskID, AgentRunID: a.agentRunID, Role: "monitor"})

	a.logger.Info().Dur("interval", a.interval).Msg("monitor agent starting")

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

	// ✅ 启动时立即评估一次（消除 6 分钟首评盲区）
	if err := a.evaluate(ctx); err != nil {
		a.logger.Error().Err(err).Msg("首次评估失败")
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Msg("monitor agent stopped")
			return ctx.Err()

		case <-ticker.C:
			if err := a.evaluate(ctx); err != nil {
				a.logger.Error().Err(err).Msg("evaluation failed")
				// 继续运行，不中断
			}
		}
	}
}

// evaluate 执行一次全局评估（使用 ReActRuntime）
func (a *Agent) evaluate(ctx context.Context) error {
	a.logger.Info().Msg("starting global evaluation with ReActRuntime")

	// 构建评估目标
	objective := a.buildEvaluationObjective()

	// 构建系统提示
	systemPrompt := a.buildSystemPrompt()

	// 配置 ReAct 运行
	config := &runtime.ReActConfig{
		Objective:     objective,
		SystemPrompt:  systemPrompt,
		LLMProvider:   a.provider,
		MaxIterations: a.maxIt, // 迭代上限 = agent.max_iterations（配置即事实；0=不设限）
		Temperature:   0.3,                        // 较低温度，确保稳定性
		MaxTokens:     4000,
		// Monitor 窗口最小（原框架 Monitor 预设口径 10）——业务预设已按分层下沉到业务侧。
		MessageModifierChain: runtime.NewDefaultModifierChain(10),

		// Checkpoint 集成
		Checkpointer:     a.checkpointer,
		CheckpointPolicy: a.checkpointPolicy,
		TaskID:           a.taskID,

		OnIteration: func(iteration int, status runtime.IterationStatus) {
			a.logger.Info().
				Str("task_id", a.taskID).
				Int("iteration", iteration).
				Str("status", string(status)).
				Msg("[MONITOR] ReAct 迭代")
		},
	}

	// 运行 ReAct 循环
	result, err := a.reactRuntime.Run(ctx, config)
	if err != nil {
		return fmt.Errorf("ReAct runtime execution: %w", err)
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
- get_global_state 的 running_actions[].running_minutes 给出每个执行中动作的已运行分钟数——超过 20 分钟的应果断 kill_action（不 kill 会占死执行通道）
- 大量 failed Actions → 考虑 replan
- 缺乏进展 → 考虑 replan

请开始评估。`, a.taskID)
}

// buildSystemPrompt 组装监察 system prompt：
// 角色章程（agent.system_prompt——DB 事实源，种子 = agents/monitor.md 正文，前端可调）
// + 空正文单句兜底。机制契约（kill_action/request_replan 决策 schema）由章程承载，
// 改契约 = 改 agents/monitor.md + make reseed。
func (a *Agent) buildSystemPrompt() string {
	if c := strings.TrimSpace(a.systemPrompt); c != "" {
		return c
	}
	return "你是监察 Agent：调 get_global_state 获取全局状态，分析指标后经 publish_decision 发布决策" +
		"（type=kill_action 需带 action_id；type=request_replan 请求重规划）。保守决策，数据驱动。"
}

// ============================================
// 辅助类型（保留用于工具内部）
// ============================================

// GlobalState 是任务的全局状态快照。
type GlobalState struct {
	Objective explorationgraph.ObjectiveNode `json:"objective"`
	Actions   []explorationgraph.Node        `json:"actions"`
	Findings  []explorationgraph.Node        `json:"findings"`

	// RunningActions 是 running 态动作的已运行时长视图——monitor 的 kill 阈值
	// 判断依据（e2e 实测：只给原始 created_at/updated_at 时间戳时，LLM 无从
	// 计算"已运行多少分钟"，20 分钟 kill 职责形同虚设）。决策变量必须显式喂给
	// 决策者，不能指望它做时间戳算术。
	RunningActions []RunningActionView `json:"running_actions"`
}

// RunningActionView 是 running 动作的监察视图。
type RunningActionView struct {
	ID             string  `json:"id"`
	Instruction    string  `json:"instruction"`
	RunningMinutes float64 `json:"running_minutes"`
}

// Decision 是监察决策。
type Decision struct {
	Type     string `json:"type"`                // "kill_action" | "request_replan"
	ActionID string `json:"action_id,omitempty"` // kill_action 需要
	Reason   string `json:"reason"`              // 决策理由
}
