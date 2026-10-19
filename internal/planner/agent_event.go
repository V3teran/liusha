package planner

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// Run 是 planner Agent 的事件循环：订阅 bus 事件 + 轮询图，规划新 action 并写图。
func (a *Agent) Run(ctx context.Context) error {
	// LLM 审计维度：规划调用归 task/本轮 run、角色 planner。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: a.taskID, AgentRunID: a.agentRunID, Role: "planner"})

	a.logger.Info().Str("task_id", a.taskID).Msg("Agent 启动")

	// 订阅事件
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 定期检查（兜底机制）
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	// 启动时规划一次
	if err := a.planActions(ctx); err != nil {
		a.logger.Error().Err(err).Msg("初始规划失败")
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止（context done）")
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止")
			return nil

		case event := <-sub.Events():
			// 处理验证结果事件
			if err := a.handleEvent(ctx, event); err != nil {
				a.logger.Error().
					Err(err).
					Str("event_type", string(event.Type)).
					Msg("事件处理失败")
			}

		case <-ticker.C:
			// 定期兜底：检查是否需要新的规划
			if err := a.planActions(ctx); err != nil {
				a.logger.Error().Err(err).Msg("定期规划失败")
			}
		}
	}
}

// handleEvent 处理事件
func (a *Agent) handleEvent(ctx context.Context, event bus.Event) error {
	switch event.Type {
	case bus.EventVerificationPassed:
		// 验证通过，可能需要新的规划
		a.logger.Info().
			Str("task_id", a.taskID).
			Msg("收到 VerificationPassed 事件，触发新规划")
		return a.planActions(ctx)

	case bus.EventVerificationRefuted:
		// 证伪，需要调整规划
		a.logger.Info().
			Str("task_id", a.taskID).
			Msg("收到 VerificationRefuted 事件，触发调整规划")
		return a.planActions(ctx)

	case bus.EventReplanRequested:
		// P2：Monitor 请求重规划
		reason := ""
		if r, ok := event.Payload["reason"].(string); ok {
			reason = r
		}
		a.logger.Info().
			Str("task_id", a.taskID).
			Str("reason", reason).
			Msg("收到 Monitor 重规划请求，强制重规划")
		return a.forcePlanActions(ctx, reason)

	default:
		// 忽略其他事件
		return nil
	}
}

// planActions 根据当前探索图生成新的 Action
