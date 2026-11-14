package planner

import (
	"context"
	"fmt"
)

func (a *Agent) forcePlanActions(ctx context.Context, reasons []string) error {
	// 直接调用 Plan，不看是否还有待执行动作；理由进规划提示，模型据此修订策略
	actions, err := a.planner.Plan(ctx, a.graph, a.taskID, PlanRequest{
		FunctionTools: a.functionTools, ReplanReasons: reasons,
	})
	if err != nil {
		return fmt.Errorf("forced plan: %w", err)
	}

	a.logger.Info().
		Int("action_count", len(actions)).
		Strs("reasons", reasons).
		Msg("强制重规划完成")

	return a.applyPlan(ctx, actions, "forced replan")
}
