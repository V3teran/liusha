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

// getCompletedActionMap 获取已完成的 Action ID 集合
func (a *Agent) getCompletedActionMap(ctx context.Context) (map[string]bool, error) {
	completedNodes, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindAction)
	if err != nil {
		return nil, fmt.Errorf("list actions: %w", err)
	}

	// 过滤出已完成的 action
	completed := make(map[string]bool)
	for _, node := range completedNodes {
		if node.State != nil && *node.State == explorationgraph.StateDone {
			completed[node.ID] = true
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
func (a *Agent) handleNoActions() {
	a.logger.Info().Msg("Planner 未生成新 Action（任务收敛）")
	// ✅ 发布任务收敛事件，触发 CompletionDetector 停止任务
	a.eventBus.PublishTaskConverged(a.taskID, "planner: no more actions to generate")
}
