// Package orchestrator 实现任务编排器（Orchestrator）。
//
// Orchestrator 是多 Agent 架构的核心，负责：
// 1. 生命周期管理：启动/停止所有 Agents
// 2. 工作流编排：协调 Agents 的执行顺序和数据流
// 3. 资源管理：持有并注入共享资源（WorldModel、Services）
//
// 架构参考：
// - Kubernetes Scheduler + Controller Manager
// - Temporal Workflow Engine
// - Ray Driver
package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/eventbus"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/monitor"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/traffic"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// Orchestrator 是任务编排器，协调所有 Agents 的执行。
type Orchestrator struct {
	taskID string

	// 共享资源（依赖注入）
	world    *knowledgegraph.Store
	traffic  *traffic.AgentStore
	findings *finding.Store
	eventBus *eventbus.Bus

	// 持续运行的 Agents
	planner *planner.Agent
	monitor *monitor.Agent

	// 按需调用的组件
	executorPool *executor.Pool
	evaluator   *evaluator.Agent

	logger zerolog.Logger
}

// Config 是 Orchestrator 的配置。
type Config struct {
	TaskID string

	// 共享资源
	World    *knowledgegraph.Store
	Traffic  *traffic.AgentStore
	Findings *finding.Store
	EventBus *eventbus.Bus

	// Agents 的依赖
	PlannerConfig  planner.Config
	MonitorConfig  monitor.Config
	ExecutorConfig executor.Config
	EvaluatorConfig evaluator.Config

	// Pool 配置
	ExecutorPoolSize int

	Logger zerolog.Logger
}

// New 创建 Orchestrator 实例。
func New(cfg Config) *Orchestrator {
	o := &Orchestrator{
		taskID:   cfg.TaskID,
		world:    cfg.World,
		traffic:  cfg.Traffic,
		findings: cfg.Findings,
		// creds:    cfg.Creds, // TODO: 暂时移除
		eventBus: cfg.EventBus,
		logger:   cfg.Logger.With().Str("component", "orchestrator").Str("task_id", cfg.TaskID).Logger(),
	}

	// 创建 Planner Agent（纯规划，持续运行）
	o.planner = planner.New(cfg.PlannerConfig)

	// 创建 Monitor Agent（独立监察，持续运行）
	o.monitor = monitor.New(cfg.MonitorConfig)

	// 创建 Executor Pool（按需调用）
	poolSize := cfg.ExecutorPoolSize
	if poolSize == 0 {
		poolSize = 10 // 默认 10 个 workers
	}
	o.executorPool = executor.NewPool(poolSize, func() *executor.Coordinator {
		return executor.NewCoordinatorFromConfig(cfg.ExecutorConfig)
	})

	// 创建 Verifier Agent（LLM 自主验证，按需调用）
	o.evaluator = evaluator.NewAgent(cfg.EvaluatorConfig)

	return o
}

// Run 启动 Orchestrator，编排整个任务的执行。
//
// 执行流程：
// 1. 启动 Planner 和 Monitor（持续运行）
// 2. 等待 Planner 初始规划完成
// 3. 主循环：
//    - 获取可执行的 Actions
//    - 执行 Actions（支持并行）
//    - 验证 Hypotheses（并行）
//    - 处理 Monitor 决策事件
//    - 检查完成条件
func (o *Orchestrator) Run(ctx context.Context) error {
	o.logger.Info().Msg("orchestrator starting")

	// 1. 启动持续运行的 Agents
	var wg sync.WaitGroup

	// 启动 Planner Agent
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := o.planner.Start(ctx); err != nil && ctx.Err() == nil {
			o.logger.Error().Err(err).Msg("planner agent stopped with error")
		}
	}()

	// 启动 Monitor Agent
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := o.monitor.Start(ctx); err != nil && ctx.Err() == nil {
			o.logger.Error().Err(err).Msg("monitor agent stopped with error")
		}
	}()

	// 2. 等待 Planner 初始规划完成
	o.logger.Info().Msg("waiting for initial planning")
	select {
	case <-o.planner.WaitInitialPlanDone():
		o.logger.Info().Msg("initial planning completed, starting execution loop")
	case <-ctx.Done():
		return ctx.Err()
	}

	// 3. 启动 Monitor 事件处理（异步）
	wg.Add(1)
	go func() {
		defer wg.Done()
		o.handleMonitorEventsAsync(ctx)
	}()

	// 4. 主编排循环
	pollInterval := 2 * time.Second
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			o.logger.Info().Msg("orchestrator stopping (context canceled)")
			// 等待 Agents 优雅退出
			wg.Wait()
			return ctx.Err()

		case <-ticker.C:
			// 4.1 检查是否完成
			if o.isComplete(ctx) {
				o.logger.Info().Msg("task completed")
				// 等待 Agents 优雅退出
				wg.Wait()
				return nil
			}

			// 4.2 获取可执行的 Actions
			actions, err := o.getExecutableActions(ctx)
			if err != nil {
				o.logger.Error().Err(err).Msg("failed to get executable actions")
				continue
			}

			if len(actions) == 0 {
				// 没有可执行的 Actions，继续等待
				continue
			}

			o.logger.Info().Int("count", len(actions)).Msg("executing actions")

			// 4.3 执行 Actions（支持并行）
			reports := o.executeActions(ctx, actions)

			// 4.4 验证 Hypotheses（并行）
			o.verifyHypotheses(ctx, reports)
		}
	}
}

// handleMonitorEventsAsync 异步处理 Monitor 决策事件
//
// 这个 goroutine 持续运行，轮询并处理 Monitor 发布的决策事件。
func (o *Orchestrator) handleMonitorEventsAsync(ctx context.Context) {
	o.logger.Info().Msg("monitor event handler started")

	// 轮询间隔：每秒检查一次
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	// 简单的内存队列（生产环境应该用持久化队列）
	// TODO: 当 eventbus 支持订阅后，改为真正的事件订阅
	for {
		select {
		case <-ctx.Done():
			o.logger.Info().Msg("monitor event handler stopped")
			return

		case <-ticker.C:
			// 处理 Monitor 事件
			// 注意：当前简化实现，生产环境应该通过 eventbus 订阅
			o.processMonitorEvents(ctx)
		}
	}
}

// processMonitorEvents 处理 Monitor 发布的事件
//
// 当前简化实现：直接从 eventbus 读取事件
// TODO: 完善事件订阅机制
func (o *Orchestrator) processMonitorEvents(ctx context.Context) {
	// 当前简化实现：暂时不处理
	// 等待 eventbus 完善订阅机制后再实现
	//
	// 预期的实现：
	// events := o.eventBus.PollEvents("monitor.*")
	// for _, event := range events {
	//     o.handleMonitorDecision(ctx, event)
	// }
}

// handleMonitorDecision 处理 Monitor 的决策事件
func (o *Orchestrator) handleMonitorDecision(ctx context.Context, event eventbus.Event) {
	switch event.Type {
	case "monitor.kill_action":
		// 终止 Action
		payload := event.Payload
		if payload == nil {
			o.logger.Warn().Msg("invalid monitor.kill_action payload")
			return
		}

		actionID, _ := payload["action_id"].(string)
		reason, _ := payload["reason"].(string)

		if actionID == "" {
			o.logger.Warn().Msg("monitor.kill_action missing action_id")
			return
		}

		o.logger.Info().
			Str("action_id", actionID).
			Str("reason", reason).
			Msg("monitor requested kill action")

		if err := o.killAction(ctx, actionID, reason); err != nil {
			o.logger.Error().Err(err).Str("action_id", actionID).Msg("failed to kill action")
		}

	case "monitor.request_replan":
		// 请求 Planner 重新规划
		payload := event.Payload
		if payload == nil {
			o.logger.Warn().Msg("invalid monitor.request_replan payload")
			return
		}

		reason, _ := payload["reason"].(string)

		o.logger.Info().
			Str("reason", reason).
			Msg("monitor requested replan")

		// 发布事件给 Planner
		o.eventBus.Publish(eventbus.Event{
			Type: "orchestrator.replan_requested",
			Payload: map[string]interface{}{
				"reason": reason,
				"source": "monitor",
			},
		})

	default:
		o.logger.Warn().Str("type", string(event.Type)).Msg("unknown monitor event type")
	}
}

// killAction 终止一个 Action
//
// 使用 CAS 确保只终止 running 状态的 Action
func (o *Orchestrator) killAction(ctx context.Context, actionID, reason string) error {
	// 构建 metadata
	metadata := fmt.Sprintf(`{"killed_by":"monitor","reason":"%s","timestamp":"%s"}`,
		reason,
		time.Now().Format(time.RFC3339))

	// 使用 CAS：只有 state=running 时才更新为 aborted
	success, err := o.world.CompareAndSwapStateWithMetadata(
		ctx,
		actionID,
		knowledgegraph.StateRunning,
		knowledgegraph.StateAborted,
		json.RawMessage(metadata),
	)

	if err != nil {
		return fmt.Errorf("CAS kill action failed: %w", err)
	}

	if !success {
		o.logger.Warn().
			Str("action_id", actionID).
			Msg("action is not in running state, cannot kill (CAS failed)")
		return nil // 不是错误，只是状态不匹配
	}

	o.logger.Info().
		Str("action_id", actionID).
		Str("reason", reason).
		Msg("action killed by monitor (CAS)")

	return nil
}

// executeActions 执行 Actions，支持并行。
//
// 执行策略：
// - 检查 Actions 之间的依赖关系
// - 无依赖：并行执行
// - 有依赖：串行执行
//
// 状态验证：
// - 执行前再次检查 Action 状态
// - 防止执行已被其他操作修改的 Actions
func (o *Orchestrator) executeActions(ctx context.Context, actions []knowledgegraph.Node) []*executor.Report {
	if len(actions) == 0 {
		return nil
	}

	// 检查是否可以并行
	if o.canExecuteParallel(actions) {
		o.logger.Info().Int("count", len(actions)).Msg("executing actions in parallel")
		return o.executeParallel(ctx, actions)
	}

	o.logger.Info().Int("count", len(actions)).Msg("executing actions sequentially")
	return o.executeSequential(ctx, actions)
}

// canExecuteParallel 检查 Actions 是否可以并行执行。
//
// 判断标准：Actions 之间无交叉依赖关系。
func (o *Orchestrator) canExecuteParallel(actions []knowledgegraph.Node) bool {
	for i, a1 := range actions {
		for j, a2 := range actions {
			if i == j {
				continue
			}

			// 检查 a1 是否依赖 a2
			for _, depID := range a1.DependsOn {
				if depID == a2.ID {
					return false
				}
			}

			// 检查 a2 是否依赖 a1
			for _, depID := range a2.DependsOn {
				if depID == a1.ID {
					return false
				}
			}
		}
	}

	return true
}

// executeParallel 并行执行多个 Actions。
//
// 状态验证：使用 CAS 原子操作确保状态一致性
func (o *Orchestrator) executeParallel(ctx context.Context, actions []knowledgegraph.Node) []*executor.Report {
	var wg sync.WaitGroup
	reportsCh := make(chan *executor.Report, len(actions))

	for _, action := range actions {
		wg.Add(1)
		go func(a knowledgegraph.Node) {
			defer wg.Done()

			// 使用 CAS 原子操作：只有 state=open 时才更新为 running
			success, err := o.world.CompareAndSwapState(ctx, a.ID,
				knowledgegraph.StateOpen, knowledgegraph.StateRunning)

			if err != nil {
				o.logger.Error().
					Err(err).
					Str("action_id", a.ID).
					Msg("CAS operation failed")
				return
			}

			if !success {
				// CAS 失败 → 状态已被其他实例修改
				o.logger.Info().
					Str("action_id", a.ID).
					Msg("action already claimed by another instance (CAS failed), skipping")
				return
			}

			// CAS 成功 → 获得了执行权
			o.logger.Info().
				Str("action_id", a.ID).
				Msg("action claimed successfully (CAS), executing")

			// 状态正确，从 Pool 获取 Executor 执行
			report := o.executorPool.Execute(ctx, a)
			if report != nil {
				reportsCh <- report
			}
		}(action)
	}

	// 等待所有 Actions 完成
	go func() {
		wg.Wait()
		close(reportsCh)
	}()

	// 收集结果
	var reports []*executor.Report
	for report := range reportsCh {
		reports = append(reports, report)
	}

	return reports
}

// executeSequential 串行执行多个 Actions。
//
// 状态验证：使用 CAS 原子操作确保状态一致性
func (o *Orchestrator) executeSequential(ctx context.Context, actions []knowledgegraph.Node) []*executor.Report {
	reports := make([]*executor.Report, 0, len(actions))

	for _, action := range actions {
		// 使用 CAS 原子操作：只有 state=open 时才更新为 running
		success, err := o.world.CompareAndSwapState(ctx, action.ID,
			knowledgegraph.StateOpen, knowledgegraph.StateRunning)

		if err != nil {
			o.logger.Error().
				Err(err).
				Str("action_id", action.ID).
				Msg("CAS operation failed")
			continue
		}

		if !success {
			// CAS 失败 → 状态已被其他实例修改
			o.logger.Info().
				Str("action_id", action.ID).
				Msg("action already claimed by another instance (CAS failed), skipping")
			continue
		}

		// CAS 成功 → 获得了执行权
		o.logger.Info().
			Str("action_id", action.ID).
			Msg("action claimed successfully (CAS), executing")

		// 状态正确，执行
		report := o.executorPool.Execute(ctx, action)
		if report != nil {
			reports = append(reports, report)
		}
	}

	return reports
}

// verifyHypotheses 验证 Hypotheses，并行处理。
//
// 流程：
// 1. 收集所有 Reports 中的 Hypotheses
// 2. 并行调用 VerifierAgent 验证
// 3. 验证通过的创建 finding
func (o *Orchestrator) verifyHypotheses(ctx context.Context, reports []*executor.Report) {
	if len(reports) == 0 {
		return
	}

	// 收集所有 Hypotheses
	var hypotheses []string
	for _, report := range reports {
		if report.Hypotheses != nil {
			hypotheses = append(hypotheses, report.Hypotheses...)
		}
	}

	if len(hypotheses) == 0 {
		return
	}

	o.logger.Info().Int("count", len(hypotheses)).Msg("verifying hypotheses in parallel")

	// 并行验证
	var wg sync.WaitGroup
	for _, hypID := range hypotheses {
		wg.Add(1)
		go func(observationID string) {
			defer wg.Done()

			// 调用 VerifierAgent（LLM 自主验证）
			finding, err := o.evaluator.Verify(ctx, observationID)
			if err != nil {
				o.logger.Error().
					Err(err).
					Str("observation_id", observationID).
					Msg("verification failed")
				return
			}

			if finding != nil {
				// 验证通过，发布事件
				o.eventBus.Publish(eventbus.Event{
					Type: "finding.verified",
					Payload: map[string]interface{}{
						"finding_id":    finding.ID,
						"observation_id": observationID,
					},
				})

				o.logger.Info().
					Str("finding_id", finding.ID).
					Str("observation_id", observationID).
					Msg("observation verified")
			} else {
				// 验证失败（refuted）
				o.logger.Info().
					Str("observation_id", observationID).
					Msg("observation refuted")
			}
		}(hypID)
	}

	wg.Wait()
}

// getExecutableActions 获取当前可执行的 Actions。
//
// 可执行条件：
// 1. state = "open"
// 2. 所有依赖的 Actions 已完成
func (o *Orchestrator) getExecutableActions(ctx context.Context) ([]knowledgegraph.Node, error) {
	// 读取所有 open actions
	allActions, err := o.world.ListOpenActions(ctx, o.taskID)
	if err != nil {
		return nil, fmt.Errorf("list open actions: %w", err)
	}

	if len(allActions) == 0 {
		return nil, nil
	}

	// 获取已完成的 action IDs
	completed, err := o.getCompletedActionIDs(ctx)
	if err != nil {
		return nil, fmt.Errorf("get completed action ids: %w", err)
	}

	// 过滤出可执行的（依赖已满足）
	var executable []knowledgegraph.Node
	for _, action := range allActions {
		if o.isDependencySatisfied(action, completed) {
			executable = append(executable, action)
		}
	}

	return executable, nil
}

// isDependencySatisfied 检查 Action 的依赖是否已满足。
func (o *Orchestrator) isDependencySatisfied(action knowledgegraph.Node, completed map[string]bool) bool {
	if len(action.DependsOn) == 0 {
		return true
	}

	for _, depID := range action.DependsOn {
		if !completed[depID] {
			return false
		}
	}

	return true
}

// getCompletedActionIDs 获取已完成的 Action IDs 集合。
func (o *Orchestrator) getCompletedActionIDs(ctx context.Context) (map[string]bool, error) {
	completedActions, err := o.world.ListCompletedActions(ctx, o.taskID)
	if err != nil {
		return nil, err
	}

	completed := make(map[string]bool, len(completedActions))
	for _, action := range completedActions {
		completed[action.ID] = true
	}

	return completed, nil
}

// isComplete 检查任务是否完成。
//
// 完成条件：
// 1. 没有 open 或 running 的 Actions
// 2. 已有至少一个 finding 或所有 Actions 都完成
func (o *Orchestrator) isComplete(ctx context.Context) bool {
	// 检查是否还有未完成的 Actions
	openActions, err := o.world.ListOpenActions(ctx, o.taskID)
	if err != nil {
		o.logger.Error().Err(err).Msg("failed to check open actions")
		return false
	}

	if len(openActions) > 0 {
		return false
	}

	runningActions, err := o.world.ListRunningActions(ctx, o.taskID)
	if err != nil {
		o.logger.Error().Err(err).Msg("failed to check running actions")
		return false
	}

	if len(runningActions) > 0 {
		return false
	}

	// 所有 Actions 都完成了
	return true
}

