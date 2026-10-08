// Package executor 提供执行层的 Agent
//
// Agent 是持续运行的异步执行器，通过事件驱动响应 Action，生成 Observation
package executor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// Agent 是事件驱动的执行器 Agent
//
// 职责：
// - 订阅 ActionProposed 事件（由 PlannerAgent 发布）
// - 执行 Action 并生成 Observation（通过 Attempt）
// - 发布 ActionCompleted 事件
// - 完全异步，不阻塞任何调用方
type Agent struct {
	graph        *explorationgraph.Store
	executor     Interface
	eventBus     bus.Bus
	logger       zerolog.Logger
	taskID       string
	maxSteps     int
	maxParallel  int         // 并发执行的 action 上限（DAG 依赖仍由 CanExecute 保证；1=串行）
	completionCh chan Report // 任务完成通知通道

	// reportMu 保护共享 Report（并行执行 action 时计数并发递增）
	reportMu sync.Mutex

	// Checkpoint 系统
	checkpointer     core.Checkpointer
	checkpointPolicy runtime.CheckpointPolicy
}

// AgentConfig 配置 Agent
type AgentConfig struct {
	TaskID       string
	Graph        *explorationgraph.Store
	Executor     Interface
	EventBus     bus.Bus
	Logger       zerolog.Logger
	MaxSteps     int         // 最大执行步数（0 表示无限制）
	MaxParallel  int         // 并发执行 action 上限（0/1=串行；DAG 依赖仍由 CanExecute 保证）
	CompletionCh chan Report // 可选：任务完成时写入 Report

	// Checkpoint 配置（可选）
	Checkpointer     core.Checkpointer
	CheckpointPolicy runtime.CheckpointPolicy
}

// NewAgent 创建 Agent
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 1000 // 默认最大步数
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = 1 // 默认串行（保守）；>1 时无依赖的 action 并发执行
	}

	return &Agent{
		graph:            cfg.Graph,
		executor:         cfg.Executor,
		eventBus:         cfg.EventBus,
		logger:           cfg.Logger.With().Str("agent", "executor").Logger(),
		taskID:           cfg.TaskID,
		maxSteps:         cfg.MaxSteps,
		maxParallel:      cfg.MaxParallel,
		completionCh:     cfg.CompletionCh,
		checkpointer:     cfg.Checkpointer,
		checkpointPolicy: cfg.CheckpointPolicy,
	}
}

// Run 启动事件循环（ctx 取消即停止）。
//
// 职责：
// - 监听事件总线上的 Action 状态变化
// - 执行可调度的 Action
// - 处理依赖关系（只执行依赖已满足的 Action）
// - 发布 ActionCompleted 事件
func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info().Str("task_id", a.taskID).Msg("Agent 启动")

	// 订阅事件
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 执行状态
	var report Report

	// 立即检查一次待执行的 Action
	if err := a.processAvailableActions(ctx, &report); err != nil {
		a.logger.Error().Err(err).Msg("初始 Action 处理失败")
	}

	// 事件循环
	ticker := time.NewTicker(constants.ToolTimeoutQuick) // 定期兜底检查
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止（context 取消）")
			report.StopWhy = stopCanceled
			a.notifyCompletion(report)
			return ctx.Err()

		case event := <-sub.Events():
			a.logger.Debug().
				Str("task_id", a.taskID).
				Str("event_type", string(event.Type)).
				Msg("收到事件")

			// 处理事件
			if err := a.handleEvent(ctx, event, &report); err != nil {
				a.logger.Error().
					Err(err).
					Str("event_type", string(event.Type)).
					Msg("事件处理失败")
			}

			// 检查停止条件
			if a.shouldStop(&report) {
				a.logger.Info().
					Str("task_id", a.taskID).
					Str("stop_why", report.StopWhy).
					Int("steps", report.Steps).
					Msg("Agent 达到停止条件")
				a.notifyCompletion(report)
				return nil
			}

		case <-ticker.C:
			// 定期兜底：检查是否有遗漏的 Action
			if err := a.processAvailableActions(ctx, &report); err != nil {
				a.logger.Error().Err(err).Msg("定期检查 Action 失败")
			}

			// 检查停止条件
			if a.shouldStop(&report) {
				a.logger.Info().
					Str("task_id", a.taskID).
					Str("stop_why", report.StopWhy).
					Int("steps", report.Steps).
					Msg("Agent 达到停止条件（定期检查）")
				a.notifyCompletion(report)
				return nil
			}
		}
	}
}

// handleEvent 处理事件
func (a *Agent) handleEvent(ctx context.Context, event bus.Event, report *Report) error {
	switch event.Type {
	case bus.EventActionProposed:
		// Planner 提议了新 Action，立即处理
		a.logger.Info().
			Str("task_id", a.taskID).
			Msg("收到 ActionProposed 事件，处理可执行 Action")
		return a.processAvailableActions(ctx, report)

	default:
		a.logger.Debug().
			Str("event_type", string(event.Type)).
			Msg("忽略事件（非 Executor 关注）")
		return nil
	}
}

// processAvailableActions 处理所有可执行的 Action
func (a *Agent) processAvailableActions(ctx context.Context, report *Report) error {
	// 获取所有 open 状态的 Action
	openActions, err := a.graph.ListOpenActions(ctx, a.taskID)
	if err != nil {
		return fmt.Errorf("list open actions: %w", err)
	}

	if len(openActions) == 0 {
		a.logger.Debug().Str("task_id", a.taskID).Msg("无待执行 Action")
		return nil
	}

	// 获取已完成的 Action ID 集合
	completed, err := a.getCompletedActionIDs(ctx)
	if err != nil {
		return fmt.Errorf("get completed actions: %w", err)
	}

	a.logger.Debug().
		Int("completed_count", len(completed)).
		Str("task_id", a.taskID).
		Msg("已完成的 Action 数量")

	// 筛选可执行的 Action（依赖已满足）
	var executable []explorationgraph.Node
	for _, action := range openActions {
		if action.CanExecute(completed) {
			executable = append(executable, action)
		}
	}

	a.logger.Info().
		Int("open_count", len(openActions)).
		Int("executable_count", len(executable)).
		Str("task_id", a.taskID).
		Msg("筛选可执行 Action")

	if len(executable) == 0 {
		a.logger.Debug().Msg("无可执行 Action（等待依赖满足）")
		return nil
	}

	// 并发执行可执行 Action：并发度 a.maxParallel（配置；1=串行）。
	// 可并行性由 DAG 决定（CanExecute 已保证依赖满足），并发度由配置决定——
	// 不由 LLM 运行时决定（业界调度惯例：依赖图 × 静态并发上限）。
	sem := make(chan struct{}, a.maxParallel)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for _, action := range executable {
		if err := ctx.Err(); err != nil {
			break
		}
		wg.Add(1)
		go func(act explorationgraph.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := a.executeAction(ctx, act, report); err != nil {
				a.logger.Error().
					Err(err).
					Str("action_id", act.ID).
					Msg("执行 Action 失败")
				// 单个失败不中止其他 Action；记录首个错误供上层感知
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(action)
	}
	wg.Wait()

	return firstErr
}

// executeAction 执行单个 Action
func (a *Agent) executeAction(
	ctx context.Context,
	action explorationgraph.Node,
	report *Report,
) error {
	a.logger.Info().
		Str("action_id", action.ID).
		Str("priority", string(action.Priority)).
		Msg("开始执行 Action")

	// 使用 CAS 标记为 running（防止并发执行同一 action）
	ok, err := a.graph.CompareAndSwapActionState(
		ctx,
		action.TaskID,
		action.ID,
		explorationgraph.StateOpen,
		explorationgraph.StateRunning,
		nil,
	)
	if err != nil {
		return fmt.Errorf("CAS to running: %w", err)
	}
	if !ok {
		// CAS 失败，说明 action 已被其他 executor 抢占
		a.logger.Info().
			Str("action_id", action.ID).
			Msg("Action 已被其他 executor 抢占，跳过")
		return nil
	}

	// 执行 Action（调用 Interface）
	attempts, execErr := a.executor.Execute(ctx, action)

	// ✅ 执行完成后检查是否已被 monitor kill（防止覆盖 aborted 状态）
	currentNode, checkErr := a.graph.GetNode(ctx, action.ID)
	if checkErr == nil && currentNode.State != nil && *currentNode.State == explorationgraph.StateAborted {
		a.logger.Warn().
			Str("action_id", action.ID).
			Msg("action 已被 monitor kill，跳过状态更新（保持 aborted）")
		// 返回原始执行错误（如果有），或特定的 killed 错误
		if execErr != nil {
			return execErr
		}
		return fmt.Errorf("action killed by monitor")
	}

	// 更新状态
	if execErr != nil {
		errMsg := execErr.Error()
		if err := a.graph.UpdateActionStateWithReason(
			ctx,
			action.ID,
			explorationgraph.StateFailed,
			&errMsg,
		); err != nil {
			a.logger.Error().Err(err).Str("action_id", action.ID).Msg("标记失败状态失败")
		}
		return execErr
	}

	// 标记为 done
	if err := a.graph.UpdateActionStateWithReason(
		ctx,
		action.ID,
		explorationgraph.StateDone,
		nil,
	); err != nil {
		a.logger.Error().Err(err).Str("action_id", action.ID).Msg("标记完成状态失败")
	}

	// 更新 report（并行执行下并发递增，加锁）
	a.reportMu.Lock()
	report.Steps++
	report.Attempts += len(attempts)
	a.reportMu.Unlock()

	// 发布所有 AttemptsGenerated 事件（通知 EvaluatorAgent）
	for _, attempt := range attempts {
		a.eventBus.PublishAttemptGenerated(a.taskID, action.ID, attempt)
	}

	// 最后发布 ActionCompleted 事件（确保 Evaluator 已收到所有 Attempt）
	a.eventBus.PublishActionCompleted(a.taskID, action.ID)

	a.logger.Info().
		Str("action_id", action.ID).
		Int("attempts_count", len(attempts)).
		Msg("Action 执行完成")

	return nil
}

// harvestObservationProposals 把指定 action 产出的、带复现配方的观察转成 Attempt。
//
// 归属判定按 action→observation 的 generates 边精确认领（write_observation 落边），
// 不再按时间窗全表扫——并行执行时窗口重叠会导致同一假设被双收割/双验证/双 finding。
// 收割层只认域信封、不解析域内形状（归一化委托 tools.NormalizeReproEnvelope），
// Primitives = 信封整体透传，复现门按 domain 分发。

// getCompletedActionIDs 获取已完成的 Action ID 集合
func (a *Agent) getCompletedActionIDs(ctx context.Context) (map[string]bool, error) {
	completed, err := a.graph.ListCompletedActions(ctx, a.taskID)
	if err != nil {
		return nil, err
	}

	result := make(map[string]bool, len(completed))
	for _, action := range completed {
		result[action.ID] = true
	}
	return result, nil
}

// shouldStop 判断是否应停止
func (a *Agent) shouldStop(report *Report) bool {
	// 达到最大步数
	if a.maxSteps > 0 && report.Steps >= a.maxSteps {
		report.StopWhy = stopMaxSteps
		return true
	}

	// TODO: 可以添加更多停止条件
	// - 无待执行 Action 且超过一定时间无新 Action
	// - 收到外部停止信号

	return false
}

// notifyCompletion 通知任务完成
func (a *Agent) notifyCompletion(report Report) {
	if a.completionCh != nil {
		select {
		case a.completionCh <- report:
			a.logger.Info().Msg("任务完成报告已发送")
		default:
			a.logger.Warn().Msg("任务完成报告发送失败（channel 已满）")
		}
	}
}
