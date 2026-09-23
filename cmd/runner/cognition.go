package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/httpreplay"
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

	if h.world == nil || taskID == "" || h.eventBus == nil {
		return executor.Report{}, fmt.Errorf("world and eventBus are required")
	}

	// 1. 创建 Registry 并注册工具
	reg := registry.New()
	tools.RegisterAll(reg, tools.Deps{
		TaskID:        taskID,
		AgentID:       assignmentID,
		Host:          host,
		Tasks:         h.tasks,
		Findings:      h.findings,
		Corpus:        h.corpus,
		Embedder:      h.embedder,
		Reranker:      h.reranker,
		Leads:         h.leads,
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
		Router:       *h.router,
		Findings:     h.findings,
		Registry:     execRegistry,
		Checkpointer: h.checkpointer,
		Logger:       h.logger,
	})

	// 4. 创建 Coordinator（使用 Engine）
	coord := executor.NewCoordinatorWithEngine(taskID, host, h.findings, engine, h.logger)

	// 3. 创建 ExecutorAgent
	executorAgent := executor.NewExecutorAgent(executor.ExecutorAgentConfig{
		TaskID:       taskID,
		World:        h.world,
		Executor:     coord,
		EventBus:     h.eventBus,
		Logger:       h.logger.With().Str("component", "executor_agent").Logger(),
		Checkpointer: h.checkpointer,
	})

	// 4. 创建 EvaluatorAgent
	replaySource := &agentTrafficScope{store: h.agentStore, taskID: taskID}
	replayer := executor.NewReplayer(replaySource)

	// 适配 finding.Store 为 evaluator.findingWriter
	findingAdapter := &findingStoreAdapter{store: h.findings}
	promoter := evaluator.New(h.world, replayer, findingAdapter)

	evaluatorAgent := evaluator.NewEvaluatorAgent(evaluator.EvaluatorAgentConfig{
		TaskID:    taskID,
		Evaluator: promoter,
		World:     h.world,
		EventBus:  h.eventBus,
		Logger:    h.logger.With().Str("component", "evaluator_agent").Logger(),
	})

	// 5. 创建 PlannerAgent
	intelligence := planner.NewIntelligence(*h.router, h.logger)

	plannerAgent := planner.NewPlannerAgent(planner.PlannerAgentConfig{
		TaskID:   taskID,
		World:    h.world,
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
		World:        h.world,
		EventBus:     h.eventBus,
		Provider:     provider,
		Interval:     6 * time.Minute,
		Logger:       h.logger.With().Str("component", "monitor_agent").Logger(),
		Checkpointer: h.checkpointer,
	})

	// 7. 创建完成检测器
	detector := cognition.NewCompletionDetector(cognition.Config{
		TaskID:              taskID,
		World:               h.world,
		Bus:                 h.eventBus,
		Logger:              h.logger,
		MaxSteps:            h.runnerCfg.MaxStepsPerTask,
		IdleRoundsThreshold: 3,
		CheckInterval:       5 * time.Second,
		GracePeriod:         30 * time.Second,
		MinExecutionDuration: 90 * time.Second, // 增加到 90 秒，给 LLM 调用足够的响应时间
	})

	// 8. 启动四个 Agent（异步）
	agentCtx, cancelAgents := context.WithCancel(ctx)
	defer cancelAgents()

	go func() {
		if err := plannerAgent.Start(agentCtx); err != nil && agentCtx.Err() == nil {
			h.logger.Error().Err(err).Str("task_id", taskID).Msg("planner agent 异常退出")
		}
	}()

	go func() {
		if err := executorAgent.Start(agentCtx); err != nil && agentCtx.Err() == nil {
			h.logger.Error().Err(err).Str("task_id", taskID).Msg("executor agent 异常退出")
		}
	}()

	go func() {
		if err := evaluatorAgent.Start(agentCtx); err != nil && agentCtx.Err() == nil {
			h.logger.Error().Err(err).Str("task_id", taskID).Msg("evaluator agent 异常退出")
		}
	}()

	go func() {
		if err := monitorAgent.Start(agentCtx); err != nil && agentCtx.Err() == nil {
			h.logger.Error().Err(err).Str("task_id", taskID).Msg("monitor agent 异常退出")
		}
	}()

	h.logger.Info().Str("task_id", taskID).Msg("四个 Agent 已启动，等待任务完成")

	// 9. 阻塞等待完成检测
	result := detector.Start(ctx)

	// 10. 优雅停止所有 Agent
	cancelAgents()
	time.Sleep(2 * time.Second)

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

// findingStoreAdapter 将 finding.Store 适配为 evaluator.findingWriter
type findingStoreAdapter struct {
	store *finding.Store
}

func (a *findingStoreAdapter) Save(ctx context.Context, f interface{}) (interface{}, error) {
	// 将 map 转换为 VulnFinding
	data, ok := f.(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("findingStoreAdapter: expected map[string]interface, got %T", f)
	}

	taskID, _ := data["task_id"].(string)
	host, _ := data["host"].(string)
	summary, _ := data["summary"].(string)
	severity, _ := data["severity"].(string)

	// Marshal evaluation 和 target 为 json.RawMessage
	var evaluation, target, repro json.RawMessage
	if evalData, ok := data["evaluation"].(map[string]interface{}); ok {
		evaluation, _ = json.Marshal(evalData)
	}
	if targetData, ok := data["target"].(map[string]interface{}); ok {
		target, _ = json.Marshal(targetData)
	}
	if reproData, ok := data["repro"].(json.RawMessage); ok {
		repro = reproData
	}

	vulnFinding := finding.VulnFinding{
		TaskID:     taskID,
		Host:       host,
		Summary:    summary,
		Severity:   severity,
		Evaluation: evaluation,
		Target:     target,
		Repro:      repro,
	}

	result, err := a.store.Save(ctx, vulnFinding)
	if err != nil {
		return nil, err
	}
	return result, nil
}
