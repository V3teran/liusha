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

	return a.applyPlan(ctx, actions, "forced replan")
}
