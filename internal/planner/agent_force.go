package planner

import (
	"context"
	"fmt"
)

func (a *Agent) forcePlanActions(ctx context.Context, monitorReason string) error {
	a.logger.Info().
		Str("monitor_reason", monitorReason).
		Msg("强制重规划（Monitor 触发）")

	// 直接调用 Plan，不检查 executable actions
	actions, err := a.planner.Plan(ctx, a.graph, a.taskID, a.functionTools)
	if err != nil {
		return fmt.Errorf("forced plan: %w", err)
	}

	a.logger.Info().
		Int("action_count", len(actions)).
		Str("monitor_reason", monitorReason).
		Msg("强制重规划完成")

	if len(actions) == 0 {
		// 与 handleNoActions 同一收敛语义：图上仍有未完结工作时，
		// "本轮无新计划"只是等待在途执行，绝不发布收敛终止任务。
		if a.hasUnsettledWork(ctx) {
			a.logger.Info().Msg("强制重规划未生成新 Action，但图上仍有未完结工作——等待其产出，不收敛")
			return nil
		}
		a.logger.Info().Msg("强制重规划未生成新 Action 且图上无未完结动作（任务收敛）")
		a.eventBus.PublishTaskConverged(a.taskID, "planner: no actions after forced replan and no unsettled work")
		return nil
	}

	// 写入探索图（复用现有逻辑）
	return a.writeActionsFromPlan(ctx, actions)
}
