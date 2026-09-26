// Package monitor 实现独立的监察 Agent（MonitorAgent）
//
// 使用 ReActRuntime 进行 LLM 评估
package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// 编译时检查接口实现
var _ core.Agent = (*Agent)(nil)

// Agent 是独立的监察 Agent。
type Agent struct {
	taskID       string
	world        *explorationgraph.Store
	eventBus     bus.Bus
	provider     llm.Provider
	reactRuntime runtime.ReActRuntime
	interval     time.Duration
	logger       zerolog.Logger

	// Checkpoint 系统
	checkpointer     core.Checkpointer
	checkpointPolicy runtime.CheckpointPolicy
}

// Config 是 Monitor Agent 的配置。
type Config struct {
	TaskID   string
	World    *explorationgraph.Store
	EventBus bus.Bus
	Provider llm.Provider
	Router   *llm.Router   // 用于获取合适的 Provider
	Interval time.Duration // 评估间隔，默认 6 分钟
	Logger   zerolog.Logger

	// Checkpoint 配置（可选）
	Checkpointer     core.Checkpointer        // nil 表示禁用 checkpoint
	CheckpointPolicy runtime.CheckpointPolicy // nil 使用默认策略
}

// New 创建 Monitor Agent 实例。
func New(cfg Config) *Agent {
	interval := cfg.Interval
	if interval == 0 {
		interval = 6 * time.Minute
	}

	// 创建 ReAct 运行时
	reactRuntime := runtime.NewReActRuntime()

	// 注册监察工具
	tools := []core.Tool{
		NewGetGlobalStateTool(cfg.World, cfg.TaskID),
		NewPublishDecisionTool(cfg.EventBus, cfg.TaskID),
	}
	for _, tool := range tools {
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
		world:            cfg.World,
		eventBus:         cfg.EventBus,
		provider:         cfg.Provider,
		reactRuntime:     reactRuntime,
		interval:         interval,
		logger:           cfg.Logger.With().Str("agent", "monitor").Str("task_id", cfg.TaskID).Logger(),
		checkpointer:     cfg.Checkpointer,
		checkpointPolicy: checkpointPolicy,
	}
}

// Start 启动 Monitor Agent，持续运行定期评估。
func (a *Agent) Start(ctx context.Context) error {
	a.logger.Info().Dur("interval", a.interval).Msg("monitor agent starting")

	ticker := time.NewTicker(a.interval)
	defer ticker.Stop()

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
		Objective:            objective,
		SystemPrompt:         systemPrompt,
		LLMProvider:          a.provider,
		MaxIterations:        10, // 监察不需要太多轮
		Temperature:          0.3,             // 较低温度，确保稳定性
		MaxTokens:            4000,
		Tools:                a.reactRuntime.GetTools(),
		MessageModifierChain: runtime.NewMonitorModifierChain(),

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
- Action 运行超过 20 分钟未完成 → 考虑 kill
- 大量 failed Actions → 考虑 replan
- 缺乏进展 → 考虑 replan

请开始评估。`, a.taskID)
}

// buildSystemPrompt 构建系统提示
func (a *Agent) buildSystemPrompt() string {
	return `你是一个监察 Agent，负责全局任务健康检查。

你的决策原则：
1. 保守决策：不确定时不要轻易 kill action
2. 数据驱动：基于具体指标做决策
3. 明确理由：每个决策都要有清晰的理由

决策类型：
- kill_action: 停止一个运行过久或明显失败的 Action
- request_replan: 请求 Planner 重新规划

决策格式示例：
{
  "type": "kill_action",
  "action_id": "act_123",
  "reason": "Action 已运行 25 分钟，超过 20 分钟阈值，且无进展"
}

请先获取全局状态，分析后做出决策。`
}

// ============================================
// 实现 framework/core.Agent 接口
// ============================================

// Name 实现 core.Agent 接口
func (a *Agent) Name() string {
	return "monitor"
}

// Run 实现 core.Agent 接口（调用现有的 Start 方法）
func (a *Agent) Run(ctx context.Context) error {
	return a.Start(ctx)
}

// Stop 实现 core.Agent 接口
func (a *Agent) Stop(ctx context.Context) error {
	a.logger.Info().Msg("stopping monitor agent")
	// Monitor 依赖 ctx.Done() 停止，无需额外操作
	return nil
}

// ExportState 实现 core.Recoverable 接口
func (a *Agent) ExportState() (json.RawMessage, error) {
	state := map[string]interface{}{
		"task_id": a.taskID,
	}
	return json.Marshal(state)
}

// ImportState 实现 core.Recoverable 接口
func (a *Agent) ImportState(data json.RawMessage) error {
	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	return nil
}

// ============================================
// 辅助类型（保留用于工具内部）
// ============================================

// GlobalState 是任务的全局状态快照。
type GlobalState struct {
	Objective explorationgraph.ObjectiveNode `json:"objective"`
	Actions   []explorationgraph.Node        `json:"actions"`
	Findings  []explorationgraph.Node        `json:"findings"`
}

// Decision 是监察决策。
type Decision struct {
	Type     string `json:"type"`                // "kill_action" | "request_replan"
	ActionID string `json:"action_id,omitempty"` // kill_action 需要
	Reason   string `json:"reason"`              // 决策理由
}
