package planner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
)

// analyzeNewResults 分析新出现的 Results
func (a *Agent) analyzeNewResults(ctx context.Context) error {
	results, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindResult)
	if err != nil {
		return fmt.Errorf("list results: %w", err)
	}

	// 分析水位落在图上（Node.IsAnalyzed，随规划修订同事务写入）：已分析的结果
	// 不再送 LLM——跨 run 同样成立，暂停后继续不会把全部结果重新分析一遍。
	fresh := make([]explorationgraph.Node, 0, len(results))
	for _, r := range results {
		if !r.IsAnalyzed() {
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
		// 修订未落图，水位随之未推进：下一轮重新分析
		return fmt.Errorf("analyze results: %w", err)
	}
	return nil
}

// hasOpenActions 判断图上是否还有待执行的动作：有则本轮不规划，等执行者消化。
// 动作之间没有执行依赖，open 即可执行。
func (a *Agent) hasOpenActions(ctx context.Context) (bool, error) {
	open, err := a.graph.ListOpenActions(ctx, a.taskID)
	if err != nil {
		return false, fmt.Errorf("list open actions: %w", err)
	}
	if len(open) > 0 {
		a.logger.Debug().Int("open_count", len(open)).Msg("已有待执行动作，跳过规划")
		return true, nil
	}
	return false, nil
}

// generateNewActions 调用 Planner 生成新的 Actions
func (a *Agent) generateNewActions(ctx context.Context) ([]explorationgraph.ActionDraft, error) {
	const (
		maxRetries = 3
		baseDelay  = 5 * time.Second
	)

	var actions []explorationgraph.ActionDraft
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			delay := baseDelay * time.Duration(1<<uint(attempt-1)) // 指数退避：5s, 10s, 20s
			a.logger.Warn().
				Err(lastErr).
				Int("attempt", attempt).
				Dur("retry_delay", delay).
				Msg("Planner 调用失败，等待重试")

			select {
			case <-time.After(delay):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}

		a.logger.Info().
			Int("attempt", attempt+1).
			Int("max_attempts", maxRetries+1).
			Msg("即将调用 a.planner.Plan()")

		actions, lastErr = a.planner.Plan(ctx, a.graph, a.taskID, a.functionTools)

		a.logger.Info().
			Bool("has_error", lastErr != nil).
			Int("actions_len", len(actions)).
			Int("attempt", attempt+1).
			Msg("a.planner.Plan() 返回")

		if lastErr == nil {
			a.logger.Info().
				Int("action_count", len(actions)).
				Int("attempts_used", attempt+1).
				Msg("Planner.Plan 成功")
			return actions, nil
		}

		// 判断是否应该重试
		if !isRetriableError(lastErr) {
			a.logger.Error().
				Err(lastErr).
				Msg("Plan() 调用失败（不可重试错误）")
			return nil, fmt.Errorf("plan: %w", lastErr)
		}

		a.logger.Warn().
			Err(lastErr).
			Int("attempt", attempt+1).
			Msg("Plan() 调用失败（可重试错误）")
	}

	a.logger.Error().
		Err(lastErr).
		Int("total_attempts", maxRetries+1).
		Msg("Plan() 调用失败（重试耗尽）")
	return nil, fmt.Errorf("plan: 重试 %d 次后仍失败: %w", maxRetries+1, lastErr)
}

// isRetriableError 判断错误是否可重试：网络抖动/限流/服务端过载/超时委托给
// 框架层 llm.IsTransient（全系统瞬时错误唯一分类事实源，含 DNS 解析失败等
// dial 类瞬时错误）；输出不可解析（ErrPlanOutputParse）同样可重试——重规划
// 是一次全新 LLM 生成，产出合法 JSON 的概率与首跑相同，放弃只会白烧已花的
// 那次调用。
func isRetriableError(err error) bool {
	return llm.IsTransient(err) || errors.Is(err, ErrPlanOutputParse)
}

// handleNoActions 处理 planner 未生成新 Action 的情况（常规规划与 monitor
// 强制重规划共用，trigger 标明来源）。
//
// "本轮无新计划" ≠ "任务收敛"：图上还有未完结工作（见 pendingWork）时，
// planner 返回空只是对在途工作的等待——发布收敛会终止整个认知循环、掐断在途
// 执行、判定与分析。只有图上再无任何未完结工作且 planner 也没有新方向，才是
// 真正的收敛。
func (a *Agent) handleNoActions(ctx context.Context, trigger string) {
	if pending := a.pendingWork(ctx); pending != "" {
		a.logger.Info().Str("trigger", trigger).Str("pending", pending).
			Msg("Planner 暂无新 Action，但图上仍有未完结工作——等其落定，不收敛")
		return
	}

	a.logger.Info().Str("trigger", trigger).Msg("Planner 未生成新 Action 且图上无未完结工作（任务收敛）")
	a.onConverged("规划者无新方向且图上无未完结工作（" + trigger + "）")
}

// pendingWork 返回阻止任务收敛的未完结工作（空串 = 可以收敛）：
//   - 非终态动作：在途执行会产出新观察；
//   - 待验证假设：带着积压收敛会留下永久悬空的假设；
//   - 未分析结果：坐实的发现还没进结果分析，它可能催生新方向、宣告方向达成。
//
// 各项判定失败都保守视为未完结（宁可多等一轮，不误终止任务）。
func (a *Agent) pendingWork(ctx context.Context) string {
	switch {
	case a.hasUnsettledWork(ctx):
		return "在途/待执行动作"
	case a.hasPendingHypotheses(ctx):
		return "待验证假设"
	case a.hasUnanalyzedResults(ctx):
		return "待分析结果"
	}
	return ""
}

// hasUnanalyzedResults 判断图上是否还有未经结果分析的 result（见
// explorationgraph.Node.IsAnalyzed）。判定失败保守返回 true。
func (a *Agent) hasUnanalyzedResults(ctx context.Context) bool {
	results, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindResult)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取 Result 失败，保守视为有待分析结果")
		return true
	}
	for _, r := range results {
		if !r.IsAnalyzed() {
			return true
		}
	}
	return false
}

// hasPendingHypotheses 判断图上是否还有待验证的假设（verification_status=pending）。
// 收敛必须等验证清零：带着积压收敛会留下永久悬空的假设（实测任务完成时
// 遗留 1 条待验证假设，无任何路径补验证）。判定失败保守返回 true。
func (a *Agent) hasPendingHypotheses(ctx context.Context) bool {
	observations, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObservation)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取 Observation 失败，保守视为有待验证假设")
		return true
	}
	for _, obs := range observations {
		if obs.HasVerificationStatus(explorationgraph.VerificationPending) {
			return true
		}
	}
	return false
}

// hasUnsettledWork 判断图上是否还有非终态 action（running/open/blocked）。
// 判定失败保守返回 true（宁可多跑，不误终止任务）。
func (a *Agent) hasUnsettledWork(ctx context.Context) bool {
	states, err := a.graph.ListActionStates(ctx, a.taskID)
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
