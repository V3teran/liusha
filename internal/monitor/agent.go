// Package monitor 实现独立的监察 Agent（MonitorAgent）- 简化版
//
// 注意：这是一个能编译通过的简化版本。
// 完整功能需要进一步完善。
package monitor

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/eventbus"
	"github.com/V3teran/liusha/internal/provider"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// Agent 是独立的监察 Agent。
type Agent struct {
	taskID   string
	world    *knowledgegraph.Store
	eventBus *eventbus.Bus
	provider provider.Provider
	interval time.Duration
	logger   zerolog.Logger
}

// Config 是 Monitor Agent 的配置。
type Config struct {
	TaskID   string
	World    *knowledgegraph.Store
	EventBus *eventbus.Bus
	Provider provider.Provider
	Interval time.Duration // 评估间隔，默认 6 分钟
	Logger   zerolog.Logger
}

// New 创建 Monitor Agent 实例。
func New(cfg Config) *Agent {
	interval := cfg.Interval
	if interval == 0 {
		interval = 6 * time.Minute
	}

	return &Agent{
		taskID:   cfg.TaskID,
		world:    cfg.World,
		eventBus: cfg.EventBus,
		provider: cfg.Provider,
		interval: interval,
		logger:   cfg.Logger.With().Str("agent", "monitor").Str("task_id", cfg.TaskID).Logger(),
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

// evaluate 执行一次全局评估（简化版）
func (a *Agent) evaluate(ctx context.Context) error {
	a.logger.Info().Msg("starting global evaluation (simplified)")

	// 1. 读取全局状态
	state, err := a.getGlobalState(ctx)
	if err != nil {
		return fmt.Errorf("get global state: %w", err)
	}

	// 2. 简化评估：检查是否有停滞的 Actions
	decisions := a.simpleEvaluation(state)

	a.logger.Info().
		Int("decisions", len(decisions)).
		Msg("evaluation completed (simplified)")

	// 3. 发布决策事件
	for _, decision := range decisions {
		a.publishDecision(decision)
	}

	return nil
}

// getGlobalState 获取任务的全局状态。
func (a *Agent) getGlobalState(ctx context.Context) (*GlobalState, error) {
	// 读取所有 Actions
	allActions, err := a.world.ListAllActions(ctx, a.taskID)
	if err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}

	// 读取所有 Findings
	findings, err := a.world.ListResults(ctx, a.taskID)
	if err != nil {
		return nil, fmt.Errorf("list findings: %w", err)
	}

	// 读取 Objective
	objective, err := a.world.GetObjective(ctx, a.taskID)
	if err != nil {
		return nil, fmt.Errorf("get objective: %w", err)
	}

	return &GlobalState{
		Objective: objective,
		Actions:   allActions,
		Findings:  findings,
	}, nil
}

// simpleEvaluation 简化的评估逻辑（不使用 LLM）
//
// TODO: 完整实现 LLM 评估
func (a *Agent) simpleEvaluation(state *GlobalState) []Decision {
	var decisions []Decision

	// 检查是否有停滞的 running Actions（运行超过 20 分钟）
	now := time.Now()
	for _, action := range state.Actions {
		if action.State == nil {
			continue
		}

		if *action.State == knowledgegraph.StateRunning {
			// 检查运行时长（使用 CreatedAt，CreatedAt 是 time.Time 非指针）
			if now.Sub(action.CreatedAt) > 20*time.Minute {
				decisions = append(decisions, Decision{
					Type:     "kill_action",
					ActionID: action.ID,
					Reason:   "Action running for more than 20 minutes without completion",
				})
			}
		}
	}

	return decisions
}

// publishDecision 发布监察决策事件。
func (a *Agent) publishDecision(decision Decision) {
	eventType := eventbus.EventType("monitor." + decision.Type)

	a.eventBus.Publish(eventbus.Event{
		Type: eventType,
		Payload: map[string]interface{}{
			"action_id": decision.ActionID,
			"reason":    decision.Reason,
			"source":    "monitor",
		},
	})

	a.logger.Info().
		Str("type", decision.Type).
		Str("action_id", decision.ActionID).
		Str("reason", decision.Reason).
		Msg("decision published")
}

// GlobalState 是任务的全局状态快照。
type GlobalState struct {
	Objective knowledgegraph.ObjectiveNode
	Actions   []knowledgegraph.Node
	Findings  []knowledgegraph.Node
}

// Decision 是监察决策。
type Decision struct {
	Type     string `json:"type"`                // "kill_action" | "request_replan"
	ActionID string `json:"action_id,omitempty"` // kill_action 需要
	Reason   string `json:"reason"`              // 决策理由
}
