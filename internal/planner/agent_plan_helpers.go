package planner

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
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
		if state == explorationgraph.StateCompleted {
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
	const (
		maxRetries = 3
		baseDelay  = 5 * time.Second
	)

	var actions []explorationgraph.Node
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

// isRetriableError 判断错误是否可重试——委托给框架层 llm.IsTransient
// （网络抖动/限流/服务端过载/超时的唯一分类事实源，含 DNS 解析失败等
// dial 类瞬时错误；业务侧不再自维护关键词表，避免两处漂移）。
func isRetriableError(err error) bool {
	return llm.IsTransient(err)
}

// handleNoActions 处理 planner 未生成新 Action 的情况。
//
// "本轮无新计划" ≠ "任务收敛"：图上还有非终态 action（running/open/blocked）
// 或**未裁决观察**时，planner 返回空只是对在途工作的等待——发布收敛会终止
// 整个认知循环、掐断在途执行与裁决。只有图上再无任何未完结工作且 planner
// 也没有新方向，才是真正的收敛。
func (a *Agent) handleNoActions(ctx context.Context) {
	if a.hasUnsettledWork(ctx) {
		a.logger.Info().Msg("Planner 暂无新 Action，但仍有在途/待执行动作——等待其产出，不收敛")
		return
	}
	if a.hasPendingVerdicts(ctx) {
		a.logger.Info().Msg("Planner 暂无新 Action，但有观察待裁决（unverified）——等裁决落定，不收敛")
		return
	}

	a.logger.Info().Msg("Planner 未生成新 Action 且图上无未完结工作（任务收敛）")
	// 收敛去重：记录结论与图指纹——之后无终态变迁的唤醒不再重复审议。
	if fp, err := a.graphFingerprint(ctx); err == nil {
		a.convergedAt, a.convergedPending = fp, true
	}
	a.eventBus.PublishTaskConverged(a.taskID, "planner: no actions and no unsettled work in graph")
}

// hasPendingVerdicts 判断图上是否还有未裁决的假设观察（confidence=unverified）。
// 收敛必须等裁决清零：带着积压收敛会留下永久悬空的假设（实测任务完成时
// 遗留 1 条 unverified，无任何路径补裁决）。判定失败保守返回 true。
func (a *Agent) hasPendingVerdicts(ctx context.Context) bool {
	observations, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObservation)
	if err != nil {
		a.logger.Warn().Err(err).Msg("读取 Observation 失败，保守视为有待裁决观察")
		return true
	}
	for _, obs := range observations {
		if obs.Confidence != nil && *obs.Confidence == explorationgraph.ConfidenceUnverified {
			return true
		}
	}
	return false
}

// graphFingerprint 是图的终态指纹：终态动作计数 + result/裁决落定计数。
// 指纹不变 = 自上轮审议以来图上没有任何新信息，重新审议必然得出同一结论。
type graphFingerprint struct {
	done, failed, aborted, exhausted int
	results                          int
	verified, refuted, facts         int
}

func (f graphFingerprint) equal(o graphFingerprint) bool {
	return f == o
}

// graphFingerprint 计算当前图终态指纹。任一计数失败返回错误（调用方据此
// 放弃去重、宁可多审议一次也不跳过）。
func (a *Agent) graphFingerprint(ctx context.Context) (graphFingerprint, error) {
	var fp graphFingerprint
	states, err := a.graph.ListActionStates(ctx, a.taskID)
	if err != nil {
		return fp, err
	}
	for _, st := range states {
		switch st {
		case explorationgraph.StateCompleted:
			fp.done++
		case explorationgraph.StateFailed:
			fp.failed++
		case explorationgraph.StateAborted:
			fp.aborted++
		case explorationgraph.StateExhausted:
			fp.exhausted++
		}
	}
	results, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindResult)
	if err != nil {
		return fp, err
	}
	fp.results = len(results)

	observations, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObservation)
	if err != nil {
		return fp, err
	}
	for _, obs := range observations {
		if obs.Confidence == nil {
			continue
		}
		switch *obs.Confidence {
		case explorationgraph.ConfidenceVerified:
			fp.verified++
		case explorationgraph.ConfidenceRefuted:
			fp.refuted++
		case explorationgraph.ConfidenceFactual:
			fp.facts++
		}
	}
	return fp, nil
}

// consumeMonitorRequest 恢复持久化的外部纠偏请求：monitor 发 replan 事件时
// 双写 objective.content.monitor_request（事件是 fire-and-forget，bus 缓冲满
// 即丢）。planner 每次规划前检查该标记，有则消费（清标记 + 强制重规划），
// 与事件路径构成 at-least-once：事件丢了大不了下一个轮询周期补上。
func (a *Agent) consumeMonitorRequest(ctx context.Context) (forced bool, reason string) {
	objectives, err := a.graph.ListNodesByKind(ctx, a.taskID, core.KindObjective)
	if err != nil || len(objectives) == 0 {
		return false, ""
	}
	var content map[string]json.RawMessage
	if json.Unmarshal(objectives[0].Content, &content) != nil {
		return false, ""
	}
	raw, ok := content["monitor_request"]
	if !ok {
		return false, ""
	}
	var req struct {
		MonitorReplanRequest struct {
			Reason string `json:"reason"`
		} `json:"monitor_replan_request"`
	}
	_ = json.Unmarshal(raw, &req)
	if req.MonitorReplanRequest.Reason == "" {
		return false, ""
	}

	// 消费即清标记（防重复触发；清失败仍强制本轮——宁可多规划一次）。
	delete(content, "monitor_request")
	if updated, err := json.Marshal(content); err == nil {
		if uErr := a.graph.UpdateNodeContent(ctx, objectives[0].ID, updated); uErr != nil {
			a.logger.Warn().Err(uErr).Str("objective_id", objectives[0].ID).
				Msg("清除 monitor 请求标记失败（可能重复触发一轮重规划）")
		}
	}
	return true, req.MonitorReplanRequest.Reason
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
