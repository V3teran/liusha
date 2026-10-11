package planner

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// analyzeNewResults 分析新出现的 Results
func (a *Agent) analyzeNewResults(ctx context.Context) error {
	results, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindResult)
	if err != nil || len(results) == 0 {
		return nil
	}

	// 水位过滤，只分析新结果
	fresh := make([]explorationgraph.Node, 0, len(results))
	for _, r := range results {
		if !a.analyzedResultIDs[r.ID] {
			fresh = append(fresh, r)
		}
	}

	if len(fresh) == 0 {
		return nil
	}

	a.logger.Info().
		Int("fresh_count", len(fresh)).
		Int("total_count", len(results)).
		Msg("检测到新 Results，先进行分析")

	if err := a.analyzeAndProcessResults(ctx, fresh); err != nil {
		a.logger.Error().Err(err).Msg("分析 Results 失败")
		// 不返回错误，继续生成 Actions
		return nil
	}

	// 标记已分析
	for _, r := range fresh {
		a.analyzedResultIDs[r.ID] = true
	}

	return nil
}

// getCompletedActionMap 获取已完成的 Action ID 集合（状态投影，不拉载荷）
func (a *Agent) getCompletedActionMap(ctx context.Context) (map[string]bool, error) {
	states, err := a.graph.ListActionStates(ctx, a.taskID)
	if err != nil {
		return nil, fmt.Errorf("list action states: %w", err)
	}

	completed := make(map[string]bool, len(states))
	for id, state := range states {
		if state == explorationgraph.StateDone {
			completed[id] = true
		}
	}

	return completed, nil
}

// getExecutableActions 获取可执行的 Actions（依赖已满足）
func getExecutableActions(openActions []explorationgraph.Node, completed map[string]bool) []explorationgraph.Node {
	var executableActions []explorationgraph.Node
	for _, action := range openActions {
		if action.CanExecute(completed) {
			executableActions = append(executableActions, action)
		}
	}
	return executableActions
}

// checkExecutableActions 检查是否已有可执行的 Action
func (a *Agent) checkExecutableActions(ctx context.Context) (bool, error) {
	openActions, err := a.graph.ListOpenActions(ctx, a.taskID)
	if err != nil {
		return false, fmt.Errorf("list open actions: %w", err)
	}

	completed, err := a.getCompletedActionMap(ctx)
	if err != nil {
		return false, err
	}

	executableActions := getExecutableActions(openActions, completed)

	if len(executableActions) > 0 {
		a.logger.Debug().
			Int("open_count", len(openActions)).
			Int("executable_count", len(executableActions)).
			Msg("已有可执行 Action，跳过规划")
		return true, nil
	}

	// 如果有 open actions 但都不可执行（全部 blocked），记录警告
	if len(openActions) > 0 {
		a.logger.Warn().
			Int("blocked_count", len(openActions)).
			Msg("所有 open actions 都被依赖阻塞，尝试生成新规划")
	}

	return false, nil
}

// generateNewActions 调用 Planner 生成新的 Actions
func (a *Agent) generateNewActions(ctx context.Context) ([]explorationgraph.Node, error) {
	a.logger.Info().Msg("即将调用 a.planner.Plan()")
	actions, err := a.planner.Plan(ctx, a.graph, a.taskID, a.functionTools)

	a.logger.Info().
		Bool("has_error", err != nil).
		Int("actions_len", len(actions)).
		Msg("a.planner.Plan() 返回")

	if err != nil {
		a.logger.Error().
			Err(err).
			Msg("Plan() 调用失败")
		return nil, fmt.Errorf("plan: %w", err)
	}

	a.logger.Info().
		Int("action_count", len(actions)).
		Msg("Planner.Plan 返回")

	return actions, nil
}

// handleNoActions 处理没有生成新 Action 的情况
// handleNoActions 处理 planner 未生成新 Action 的情况。
//
// "本轮无新计划" ≠ "任务收敛"：图上还有非终态 action（running/open/blocked）
// 时，planner 返回空只是对在途执行的等待——发布收敛会终止整个认知循环、
// 掐断在途执行与裁决。只有图上再无任何未完结工作且 planner 也没有新方向，
// 才是真正的收敛。
func (a *Agent) handleNoActions() {
	if a.hasUnsettledWork() {
		a.logger.Info().Msg("Planner 暂无新 Action，但仍有在途/待执行动作——等待其产出，不收敛")
		return
	}

	a.logger.Info().Msg("Planner 未生成新 Action 且图上无未完结动作（任务收敛）")
	a.eventBus.PublishTaskConverged(a.taskID, "planner: no actions and no unsettled work in graph")
}

// hasUnsettledWork 判断图上是否还有非终态 action（running/open/blocked）。
// 判定失败保守返回 true（宁可多跑，不误终止任务）。
func (a *Agent) hasUnsettledWork() bool {
	states, err := a.graph.ListActionStates(context.Background(), a.taskID)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取 action 状态失败，保守视为仍有未完结工作")
		return true
	}
	for _, state := range states {
		if !state.IsTerminal() {
			return true
		}
	}
	return false
}
