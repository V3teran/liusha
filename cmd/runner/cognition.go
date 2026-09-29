package main

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/httpreplay"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/monitor"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/traffic"
)

// agentTrafficScope scopes AgentStore reads to a single task.
// Satisfies executor.TrafficSource: returns (Source, ok, err) where ok=false
// when the record does not belong to the task.
type agentTrafficScope struct {
	store  *traffic.AgentStore
	taskID string
}

func (s *agentTrafficScope) GetInScope(ctx context.Context, id int64) (httpreplay.Source, bool, error) {
	f, err := s.store.GetByID(ctx, id)
	if err != nil {
		return httpreplay.Source{}, false, err
	}
	if f.TaskID != s.taskID {
		return httpreplay.Source{}, false, nil
	}
	return httpreplay.Source{
		ID:      f.ID,
		Method:  f.Method,
		URL:     f.URL,
		Headers: f.RequestHeaders,
		Body:    f.RequestBody,
	}, true, nil
}

// runCognition drives a single engagement through the L4 cognition loop:
// four independent agents (Planner, Executor, Evaluator, Monitor) coordinate via bus.Bus.
func (h handler) runCognition(
	ctx context.Context,
	assignmentID, taskID, host string,
) (executor.Report, error) {
	h.logger.Info().
		Str("task_id", taskID).
		Msg("[RUN_COGNITION] 启动四Agent架构")

	if h.graph == nil || taskID == "" || h.eventBus == nil {
		return executor.Report{}, fmt.Errorf("graph and eventBus are required")
	}

	// 1. 创建 Registry 并注册工具；挂工具遥测 + 任务心跳 interceptor——
	// 每次工具调用落 tool_invocation 并节流续命 task.heartbeat_at（reaper 判活依据）。
	reg := registry.New()
	reg.AddInterceptor(h.toolRecordInterceptor(assignmentID, taskID))
	tools.RegisterAll(reg, tools.Deps{
		TaskID:        taskID,
		AgentID:       assignmentID,
		Host:          host,
		Tasks:         h.tasks,
		Findings:      h.findings,
		Corpus:        h.corpus,
		Embedder:      h.embedder,
		Reranker:      h.reranker,
		Insights:      h.insights,
		ProxyStore:    h.proxyStore,
		AgentStore:    h.agentStore,
		Creds:         h.creds,
		Sandbox:       nil, // Sandbox 由 sandboxMgr 管理
		ToolingLoader: h.toolingLoader,
		VulnLoader:    h.vulnLoader,
	})

	// 2. 包装为 executor.Registry
	execRegistry := executor.NewRegistry(reg)

	// 3. 创建 Executor Engine
	engine := executor.NewEngine(executor.EngineConfig{
		Router:       h.router,
		Findings:     h.findings,
		Registry:     execRegistry,
		Checkpointer: h.checkpointer,
		Logger:       h.logger,
	})

	// 4. 创建 Coordinator（使用 Engine）
	coord := executor.NewCoordinatorWithEngine(taskID, host, h.findings, engine, h.logger)

	// 3. 创建 ExecutorAgent
	executorAgent := executor.NewAgent(executor.AgentConfig{
		TaskID:       taskID,
		Graph:        h.graph,
		Executor:     coord,
		EventBus:     h.eventBus,
		Logger:       h.logger.With().Str("component", "executor_agent").Logger(),
		Checkpointer: h.checkpointer,
	})

	// 4. 创建 EvaluatorAgent
	replaySource := &agentTrafficScope{store: h.agentStore, taskID: taskID}
	replayer := executor.NewReplayer(replaySource)

	promoter := evaluator.New(h.graph, replayer, h.findings)

	evaluatorAgent := evaluator.NewAgent(evaluator.AgentConfig{
		TaskID:    taskID,
		Evaluator: promoter,
		EventBus:  h.eventBus,
		Logger:    h.logger.With().Str("component", "evaluator_agent").Logger(),
	})

	// 5. 创建 PlannerAgent
	intelligence := planner.NewIntelligence(h.router, h.logger)

	plannerAgent := planner.NewAgent(planner.AgentConfig{
		TaskID:   taskID,
		Graph:    h.graph,
		Planner:  intelligence,
		EventBus: h.eventBus,
		Logger:   h.logger.With().Str("component", "planner_agent").Logger(),
	})

	// 6. 创建 MonitorAgent
	provider, err := h.router.For(ctx, "medium")
	if err != nil {
		return executor.Report{}, fmt.Errorf("failed to get provider for monitor: %w", err)
	}

	monitorAgent := monitor.New(monitor.Config{
		TaskID:       taskID,
		Graph:        h.graph,
		EventBus:     h.eventBus,
		Provider:     provider,
		Interval:     6 * time.Minute,
		Logger:       h.logger.With().Str("component", "monitor_agent").Logger(),
		Checkpointer: h.checkpointer,
	})

	// 7. 创建完成检测器（持续探索模式）
	detector := cognition.NewCompletionDetector(cognition.Config{
		TaskID:        taskID,
		Graph:         h.graph,
		Bus:           h.eventBus,
		Logger:        h.logger,
		MaxSteps:      h.runnerCfg.MaxStepsPerTask, // 0 表示无限制
		CheckInterval: 5 * time.Second,
	})

	// 8. 启动四个 Agent（异步；启停封装成闭包供控制平面 pause/resume 复用）
	var agentWg sync.WaitGroup
	var agentCtx context.Context
	var cancelAgents context.CancelFunc

	runAgent := func(name string, start func(context.Context) error) {
		defer agentWg.Done()
		if err := start(agentCtx); err != nil && agentCtx.Err() == nil {
			h.logger.Error().Err(err).Str("task_id", taskID).Msg(name + " agent 异常退出")
		}
	}
	goAgent := func(name string, start func(context.Context) error) {
		agentWg.Add(1)
		logx.Go(h.logger, name+"-agent", func() { runAgent(name, start) })
	}

	agents := &controlAgentLifecycle{
		start: func() {
			agentCtx, cancelAgents = context.WithCancel(ctx) //nolint:gosec // cancelAgents 由 stop() 闭包保证调用
			goAgent("planner", plannerAgent.Run)
			goAgent("executor", executorAgent.Run)
			goAgent("evaluator", evaluatorAgent.Run)
			goAgent("monitor", monitorAgent.Run)
		},
		stop: func() {
			cancelAgents()
			agentWg.Wait()
		},
	}

	agents.start()
	defer agents.stop()

	// 8.5 控制平面消费者：轮询 task_control_event（pause/resume/terminate/adjust_goal/inject）
	if h.controlPlane != nil {
		stopConsumer := startControlConsumer(ctx, taskID, h.controlPlane, h.graph, detector, agents, h.logger)
		defer stopConsumer()
	}

	h.logger.Info().Str("task_id", taskID).Msg("四个 Agent 已启动，等待任务完成")

	// 9. 阻塞等待完成检测
	result := detector.Start(ctx)

	// 10. 优雅停止所有 Agent
	agents.stop()

	h.logger.Info().
		Str("task_id", taskID).
		Int("steps", result.Steps).
		Int("promoted", result.Promoted).
		Str("stop_why", result.StopWhy).
		Dur("duration", result.Duration).
		Msg("认知循环完成")

	return executor.Report{
		Steps:    result.Steps,
		Promoted: result.Promoted,
		Attempts: result.Attempts,
		StopWhy:  result.StopWhy,
	}, nil
}
