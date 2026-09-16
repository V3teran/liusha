package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/knowledgegraph"
	"github.com/V3teran/liusha/internal/monitor"
	"github.com/V3teran/liusha/internal/planner"
)

// runWithGraph 使用 Graph 编排执行任务
//
// 架构对比：
// - 旧 Orchestrator: 2秒轮询 + 命令式控制流
// - 新 Graph: 事件驱动 + 声明式依赖图
//
// Graph 拓扑：
//   initialize → start_agents → fetch_actions → execute_actions → check_completion
//                     ↑                                                  ↓
//                     └──────────────── continue ────────────────────────┘
func (h handler) runWithGraph(ctx context.Context, agentID, taskID, virtualHost string) error {
	h.logger.Info().
		Str("task_id", taskID).
		Str("agent_id", agentID).
		Msg("启动任务（Graph 编排）")

	// 创建事件总线
	actionBus := core.New()
	plannerEventBus := executor.NewPlannerEventBus(ctx)

	// 获取 Provider
	complexProvider, err := h.router.For(ctx, llm.ComplexityComplex)
	if err != nil {
		return fmt.Errorf("failed to get complex provider: %w", err)
	}

	// 创建 Planner Agent
	plannerAgent := planner.New(planner.Config{
		TaskID:       taskID,
		EventBus:     plannerEventBus,
		ActionBus:    actionBus,
		World:        h.world,
		ControlPlane: h.controlPlane,
		Router:       h.router,
		Logger:       h.logger,
	})

	// 创建 Monitor Agent
	monitorAgent := monitor.New(monitor.Config{
		TaskID:   taskID,
		World:    h.world,
		EventBus: actionBus,
		Provider: complexProvider,
		Interval: 0, // 使用默认 6 分钟
		Logger:   h.logger,
	})

	// 创建 Executor Pool
	executorConfig := executor.Config{
		TaskID:       taskID,
		Host:         virtualHost,
		World:        h.world,
		TrafficStore: h.agentStore,
		ProxyStore:   h.proxyStore,
		FindingStore: h.findings,
		ControlPlane: h.controlPlane,
		Router:       h.router,
		Registry:     h.buildExecutorRegistry(agentID, taskID, virtualHost),
		AgentFunc:    nil,
		Logger:       h.logger,
	}
	executorPool := executor.NewPool(10, func() *executor.Coordinator {
		return executor.NewCoordinatorFromConfig(executorConfig)
	})

	// 创建 AgentRunner（管理 Planner 和 Monitor 生命周期）
	agentRunner := core.NewAgentRunner(core.AgentRunnerConfig{
		Logger:       h.logger,
		StartTimeout: 30 * time.Second,
		StopTimeout:  10 * time.Second,
	})
	if err := agentRunner.AddAgent(plannerAgent); err != nil {
		return fmt.Errorf("failed to add planner to runner: %w", err)
	}
	if err := agentRunner.AddAgent(monitorAgent); err != nil {
		return fmt.Errorf("failed to add monitor to runner: %w", err)
	}

	// 构建 Graph
	config := &core.GraphConfig{
		ExecutionMode: core.ExecutionModeSequential, // 串行模式
		MaxIterations: 1000,                         // 最多 1000 次循环
	}
	graph := core.NewGraph("initialize", config)

	// 共享状态（线程安全）
	var (
		agentsMu        sync.Mutex
		agentsStarted   bool
		iterationCount  int
		lastActionCount int
	)

	// 节点 1: initialize - 初始化任务
	graph.AddNode("initialize", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		h.logger.Info().Str("task_id", taskID).Msg("Graph 初始化")

		state.Set("task_id", taskID)
		state.Set("iteration", 0)
		state.Set("should_continue", true)

		return state, nil
	})

	// 节点 2: start_agents - 启动 Planner 和 Monitor（只执行一次）
	graph.AddNode("start_agents", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		agentsMu.Lock()
		defer agentsMu.Unlock()

		if agentsStarted {
			// 已启动，跳过
			return state, nil
		}

		h.logger.Info().Msg("启动 AgentRunner（Planner + Monitor）")

		// 启动 AgentRunner
		if err := agentRunner.Start(ctx); err != nil {
			return state, fmt.Errorf("failed to start agent runner: %w", err)
		}

		agentsStarted = true

		// 等待 Planner 初始规划完成
		h.logger.Info().Msg("等待 Planner 初始规划完成...")
		select {
		case <-plannerAgent.WaitInitialPlanDone():
			h.logger.Info().Msg("Planner 初始规划完成")
		case <-time.After(30 * time.Second):
			return state, fmt.Errorf("planner initial planning timeout")
		case <-ctx.Done():
			return state, ctx.Err()
		}

		state.Set("agents_started", true)

		return state, nil
	})

	// 节点 3: fetch_actions - 从 WorldModel 获取可执行的 Actions
	graph.AddNode("fetch_actions", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		iteration := iterationCount
		iterationCount++

		h.logger.Debug().
			Int("iteration", iteration).
			Msg("获取可执行 Actions")

		// 1. 读取所有 open actions
		allActions, err := h.world.ListOpenActions(ctx, taskID)
		if err != nil {
			return state, fmt.Errorf("list open actions: %w", err)
		}

		h.logger.Debug().
			Int("open_count", len(allActions)).
			Msg("查询到 open Actions")

		// 2. 获取已完成的 action IDs
		completed, err := h.getCompletedActionIDs(ctx, taskID)
		if err != nil {
			return state, fmt.Errorf("get completed action ids: %w", err)
		}

		// 3. 过滤出可执行的（依赖已满足）
		var executable []knowledgegraph.Node
		for _, action := range allActions {
			if h.isDependencySatisfied(action, completed) {
				executable = append(executable, action)
			}
		}

		h.logger.Info().
			Int("iteration", iteration).
			Int("open", len(allActions)).
			Int("executable", len(executable)).
			Int("completed", len(completed)).
			Msg("依赖解析完成")

		state.Set("executable_actions", executable)
		state.Set("action_count", len(executable))

		// 记录连续无 Action 的次数（用于日志）
		if len(executable) == 0 {
			lastActionCount++
		} else {
			lastActionCount = 0
		}

		return state, nil
	})

	// 节点 4: execute_actions - 执行 Actions
	graph.AddNode("execute_actions", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		actionsRaw, _ := state.Get("executable_actions")
		actions, ok := actionsRaw.([]knowledgegraph.Node)
		if !ok || len(actions) == 0 {
			h.logger.Debug().Msg("没有可执行的 Actions")
			state.Set("has_work", false)
			return state, nil
		}

		h.logger.Info().
			Int("count", len(actions)).
			Msg("开始执行 Actions")

		// 执行 Actions（支持并行/串行）
		reports := h.executeActionsWithGraph(ctx, actions, executorPool)

		h.logger.Info().
			Int("executed", len(reports)).
			Msg("Actions 执行完成")

		// 发布完成事件（触发 Planner 重新规划）
		for _, report := range reports {
			if report != nil {
				plannerEventBus.Publish(executor.Event{
					Type:   executor.EventActionCompleted,
					TaskID: taskID,
				})
			}
		}

		// 验证 Hypotheses（如果需要）
		h.verifyHypothesesWithGraph(ctx, reports)

		state.Set("has_work", true)
		state.Set("reports", reports)

		return state, nil
	})

	// 节点 5: check_completion - 检查任务是否完成
	graph.AddNode("check_completion", func(ctx context.Context, state core.GraphState) (core.GraphState, error) {
		// 检查是否还有未完成的 Actions
		openActions, err := h.world.ListOpenActions(ctx, taskID)
		if err != nil {
			h.logger.Error().Err(err).Msg("检查 open actions 失败")
			return state, err
		}

		runningActions, err := h.world.ListRunningActions(ctx, taskID)
		if err != nil {
			h.logger.Error().Err(err).Msg("检查 running actions 失败")
			return state, err
		}

		completed := len(openActions) == 0 && len(runningActions) == 0

		h.logger.Debug().
			Int("open", len(openActions)).
			Int("running", len(runningActions)).
			Bool("completed", completed).
			Msg("任务状态检查")

		state.Set("task_completed", completed)

		return state, nil
	})

	// 边：initialize → start_agents
	graph.AddEdge("initialize", "start_agents")

	// 边：start_agents → fetch_actions
	graph.AddEdge("start_agents", "fetch_actions")

	// 条件边：fetch_actions → execute_actions 或 check_completion
	graph.AddConditionalEdge("fetch_actions", func(ctx context.Context, state core.GraphState) (string, error) {
		actionCount, _ := state.GetInt("action_count")
		if actionCount > 0 {
			return "execute", nil
		}
		return "check", nil
	}, map[string]string{
		"execute": "execute_actions",
		"check":   "check_completion",
	})

	// 边：execute_actions → check_completion
	graph.AddEdge("execute_actions", "check_completion")

	// 条件边：check_completion → fetch_actions（继续）或 END（完成）
	graph.AddConditionalEdge("check_completion", func(ctx context.Context, state core.GraphState) (string, error) {
		completed, _ := state.GetBool("task_completed")
		if completed {
			return "end", nil
		}

		// 任务未完成，继续下一轮
		// 添加短暂延迟，避免过于频繁的轮询
		time.Sleep(2 * time.Second)
		return "continue", nil
	}, map[string]string{
		"continue": "fetch_actions",
		"end":      core.END,
	})

	// 编译 Graph
	if err := graph.Compile(); err != nil {
		return fmt.Errorf("failed to compile graph: %w", err)
	}

	h.logger.Info().Msg("Graph 编译完成，开始执行")

	// 运行 Graph
	input := core.NewGraphState()
	finalState, err := graph.Run(ctx, *input)
	if err != nil {
		h.logger.Error().Err(err).Msg("Graph 执行失败")

		// 停止 Agents
		stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if stopErr := agentRunner.Stop(stopCtx); stopErr != nil {
			h.logger.Error().Err(stopErr).Msg("停止 Agents 失败")
		}

		return fmt.Errorf("graph execution failed: %w", err)
	}

	// 优雅停止 Agents
	h.logger.Info().Msg("任务完成，停止 Agents")
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := agentRunner.Stop(stopCtx); err != nil {
		h.logger.Error().Err(err).Msg("停止 Agents 失败")
	}

	h.logger.Info().
		Str("task_id", taskID).
		Int("total_iterations", iterationCount).
		Msg("任务完成")

	// 输出最终状态（调试用）
	_ = finalState

	return nil
}

// executeActionsWithGraph 执行 Actions（支持并行/串行）
func (h handler) executeActionsWithGraph(
	ctx context.Context,
	actions []knowledgegraph.Node,
	pool *executor.Pool,
) []*executor.Report {
	if len(actions) == 0 {
		return nil
	}

	// 检查是否可以并行
	if h.canExecuteParallel(actions) {
		h.logger.Info().Int("count", len(actions)).Msg("并行执行 Actions")
		return h.executeParallel(ctx, actions, pool)
	}

	h.logger.Info().Int("count", len(actions)).Msg("串行执行 Actions")
	return h.executeSequential(ctx, actions, pool)
}

// canExecuteParallel 检查 Actions 是否可以并行执行
func (h handler) canExecuteParallel(actions []knowledgegraph.Node) bool {
	if len(actions) <= 1 {
		return true
	}

	// 构建当前批次的 action ID 集合
	actionIDs := make(map[string]bool, len(actions))
	for _, action := range actions {
		actionIDs[action.ID] = true
	}

	// 检查是否存在内部依赖
	for _, action := range actions {
		for _, depID := range action.DependsOn {
			if actionIDs[depID] {
				// 发现内部依赖，不能并行
				return false
			}
		}
	}

	return true
}

// executeParallel 并行执行多个 Actions
func (h handler) executeParallel(
	ctx context.Context,
	actions []knowledgegraph.Node,
	pool *executor.Pool,
) []*executor.Report {
	var wg sync.WaitGroup
	reportsCh := make(chan *executor.Report, len(actions))

	for _, action := range actions {
		wg.Add(1)
		go func(a knowledgegraph.Node) {
			defer wg.Done()

			// 使用 CAS 原子操作：只有 state=open 时才更新为 running
			success, err := h.world.CompareAndSwapState(ctx, a.ID,
				knowledgegraph.StateOpen, knowledgegraph.StateRunning)

			if err != nil {
				h.logger.Error().
					Err(err).
					Str("action_id", a.ID).
					Msg("CAS 操作失败")
				return
			}

			if !success {
				// CAS 失败 → 状态已被其他实例修改
				h.logger.Info().
					Str("action_id", a.ID).
					Msg("Action 已被其他实例认领（CAS 失败），跳过")
				return
			}

			// CAS 成功 → 获得了执行权
			h.logger.Info().
				Str("action_id", a.ID).
				Msg("Action 认领成功（CAS），开始执行")

			// 从 Pool 获取 Executor 执行
			report := pool.Execute(ctx, a)
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

// executeSequential 串行执行多个 Actions
func (h handler) executeSequential(
	ctx context.Context,
	actions []knowledgegraph.Node,
	pool *executor.Pool,
) []*executor.Report {
	var reports []*executor.Report

	for _, action := range actions {
		// 使用 CAS 原子操作
		success, err := h.world.CompareAndSwapState(ctx, action.ID,
			knowledgegraph.StateOpen, knowledgegraph.StateRunning)

		if err != nil {
			h.logger.Error().
				Err(err).
				Str("action_id", action.ID).
				Msg("CAS 操作失败")
			continue
		}

		if !success {
			h.logger.Info().
				Str("action_id", action.ID).
				Msg("Action 已被其他实例认领（CAS 失败），跳过")
			continue
		}

		// 执行
		report := pool.Execute(ctx, action)
		if report != nil {
			reports = append(reports, report)
		}
	}

	return reports
}

// verifyHypothesesWithGraph 验证 Hypotheses（简化实现）
func (h handler) verifyHypothesesWithGraph(ctx context.Context, reports []*executor.Report) {
	// TODO: 实现 Hypothesis 验证逻辑
	// 当前简化：跳过验证
	if len(reports) == 0 {
		return
	}

	h.logger.Debug().
		Int("report_count", len(reports)).
		Msg("Hypothesis 验证（当前跳过）")
}

// getCompletedActionIDs 获取已完成的 Action IDs 集合
func (h handler) getCompletedActionIDs(ctx context.Context, taskID string) (map[string]bool, error) {
	completedActions, err := h.world.ListCompletedActions(ctx, taskID)
	if err != nil {
		return nil, err
	}

	completed := make(map[string]bool, len(completedActions))
	for _, action := range completedActions {
		completed[action.ID] = true
	}

	return completed, nil
}

// isDependencySatisfied 检查 Action 的依赖是否已满足
func (h handler) isDependencySatisfied(action knowledgegraph.Node, completed map[string]bool) bool {
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
