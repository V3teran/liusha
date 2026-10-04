package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/logx"
	"github.com/V3teran/liusha/internal/monitor"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/google/uuid"
)

// runCognition drives a single assignment through the L4 cognition loop:
// four independent agents (Planner, Executor, Evaluator, Monitor) coordinate via bus.Bus.
// sb 是本任务独占的沙箱客户端；nil 表示无沙箱（run_command/browser_use 不注册）。
func (h handler) runCognition(
	ctx context.Context,
	assignmentID, taskID, host, brief string,
	sb sandbox.Client,
) (executor.Report, error) {
	h.logger.Info().
		Str("task_id", taskID).
		Str("brief", brief).
		Msg("[RUN_COGNITION] 启动四Agent架构")

	if h.graph == nil || taskID == "" || h.eventBus == nil {
		return executor.Report{}, fmt.Errorf("graph and eventBus are required")
	}

	// ========== 创建初始 Objective 节点（幂等：onboard 已建则跳过，防双根） ==========
	if brief != "" {
		if existing, listErr := h.graph.ListNodesByKind(ctx, taskID, core.KindObjective); listErr == nil && len(existing) > 0 {
			h.logger.Info().
				Str("task_id", taskID).
				Int("existing_objectives", len(existing)).
				Msg("根 Objective 已存在（onboard 创建），跳过重复创建")
		} else {
			rootID := uuid.New().String()
			contentJSON, _ := json.Marshal(map[string]string{"brief": brief})

			// 根据数据库约束，objective 类型节点必须有非空 state
			initialState := explorationgraph.StateOpen

			objectiveNode := explorationgraph.Node{
				ID:         rootID,
				TaskID:     taskID,
				Kind:       core.KindObjective,
				Content:    contentJSON,
				State:      &initialState, // 必须设置 state
				Priority:   explorationgraph.PriorityHigh,
				SourceType: explorationgraph.SourceUser,
				SourceID:   "task_init",
			}

			createdID, err := h.graph.CreateNode(ctx, objectiveNode)
			if err != nil {
				return executor.Report{}, fmt.Errorf("failed to create root objective: %w", err)
			}
			h.logger.Info().
				Str("task_id", taskID).
				Str("root_id", createdID).
				Str("brief", brief).
				Msg("✅ 已创建根 Objective 节点")
		}
	} else {
		h.logger.Warn().
			Str("task_id", taskID).
			Msg("⚠️  任务 brief 为空，跳过创建根 Objective")
	}
	// ========================================================

	// ========== 加载四 Agent 配置（function_tools/cli_tools 白名单的事实源） ==========
	getCfg := func(code string) agent.Agent {
		cfg, err := h.agentCfgStore.GetByCode(ctx, code)
		if err != nil {
			h.logger.Warn().Err(err).Str("code", code).Msg("⚠️  加载 agent 配置失败，使用空配置")
			return agent.Agent{Code: code}
		}
		return cfg
	}
	executorCfg := getCfg("executor")
	plannerCfg := getCfg("planner")
	evaluatorCfg := getCfg("evaluator")
	monitorCfg := getCfg("monitor")

	h.logger.Info().
		Strs("cli_tools", executorCfg.CliTools).
		Int("cli_tools_count", len(executorCfg.CliTools)).
		Msg("✅ Executor Agent 配置已加载")

	// ========== 新增：根据白名单过滤工具清单 ==========
	var filteredManifest *manifest.Manifest
	if h.toolsManifest != nil && len(executorCfg.CliTools) > 0 {
		// ✅ 只保留白名单中的工具
		filteredManifest = h.toolsManifest.FilterByNames(executorCfg.CliTools)

		h.logger.Info().
			Int("total_in_yaml", len(h.toolsManifest.Tools)).
			Int("whitelisted", len(executorCfg.CliTools)).
			Int("filtered_result", len(filteredManifest.Tools)).
			Strs("tool_names", filteredManifest.Names()).
			Msg("✅ CLI 工具已过滤（白名单机制）")
	} else {
		filteredManifest = &manifest.Manifest{Tools: []manifest.Tool{}}
		h.logger.Warn().Msg("⚠️  未配置 cli_tools 或 tools.yaml 未加载，外部工具不可用")
	}
	// ========================================================

	// 1. 创建 Registry 并注册工具；挂工具遥测 + 任务心跳 interceptor——
	// 每次工具调用落 tool_invocation 并节流续命 task.heartbeat_at（reaper 判活依据）。
	// ExecutorID 口径：tool_invocation.agent_task_id 外键指向 agent_run.id，
	// 取本 task 的 run 行（api expandItem 建的那条）；查不到留空 → NULL。
	agentRunID := ""
	if runs, rErr := h.executors.ListByTask(ctx, taskID, 1); rErr == nil && len(runs) > 0 {
		agentRunID = runs[0].ID
	} else if rErr != nil {
		h.logger.Warn().Err(rErr).Str("task_id", taskID).Msg("查询 agent_run 失败，tool_invocation 将不关联 run")
	}

	reg := registry.New()
	reg.AddInterceptor(h.toolRecordInterceptor(agentRunID, taskID))
	tools.RegisterAll(reg, tools.Deps{
		TaskID:        taskID,
		AgentID:       agentRunID,
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
		Sandbox:       sb,      // run_command/browser_use 依赖；nil 时这两个工具不注册
		Graph:         h.graph, // write_observation/write_evidence 依赖——晋升提议权的载体
		ToolingLoader: h.toolingLoader,
		VulnLoader:    h.vulnLoader,
	})

	// 2. evaluator 裁决官的跨包工具：按其 function_tools 白名单从 BuildTools 取
	// （run_command/list_traffic/view_traffic 等来自 tools 包；replay_for_verification 在 judge 本地）
	var evaluatorCLImanifest *manifest.Manifest
	if h.toolsManifest != nil && len(evaluatorCfg.CliTools) > 0 {
		evaluatorCLImanifest = h.toolsManifest.FilterByNames(evaluatorCfg.CliTools)
	}
	judge := evaluator.NewRouterJudge(h.router, h.logger).
		WithFunctionTools(evaluatorCfg.FunctionTools).
		WithCLIManifest(evaluatorCLImanifest).
		WithExtraTools(tools.BuildTools(tools.Deps{
			TaskID:        taskID,
			AgentID:       agentRunID,
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
			Sandbox:       sb, // evaluator 的 CLI 复核通道（run_command）
			ToolingLoader: h.toolingLoader,
			VulnLoader:    h.vulnLoader,
		}, evaluatorCfg.FunctionTools))

	// 3. 包装为 executor.Registry
	execRegistry := executor.NewRegistry(reg)

	// 3. 创建 Executor Engine
	engine := executor.NewEngine(executor.EngineConfig{
		Router:        h.router,
		Findings:      h.findings,
		Registry:      execRegistry,
		FunctionTools: executorCfg.FunctionTools, // function_tools 白名单（nil=全量）
		ToolsManifest: filteredManifest,          // CLI 工具清单（白名单过滤后）
		Brief:         brief,                     // 任务简报逐字进 executor system prompt（入口锚定）
		Checkpointer:  h.checkpointer,
		Logger:        h.logger,
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
		MaxParallel:  4, // 无依赖 action 并发执行（DAG 定可并行性；浏览器已按 action 隔离 tab）
	})

	// 4. 创建 EvaluatorAgent：Replayer 零依赖（复现配方自包含完整 HTTP 请求，
	// 不从 traffic 表取流量——工具自发流量同样可复现）。
	replayer := executor.NewReplayer()

	promoter := evaluator.New(h.graph, replayer, h.findings).
		WithJudge(judge). // ReAct 裁决官：replay_for_verification + CLI 复核工具
		WithLogger(h.logger).
		WithAgentRunID(agentRunID)

	evaluatorAgent := evaluator.NewAgent(evaluator.AgentConfig{
		TaskID:    taskID,
		Evaluator: promoter,
		EventBus:  h.eventBus,
		Logger:    h.logger.With().Str("component", "evaluator_agent").Logger(),
		// 验证并发：每个验证 = 机器复放 + 一轮 judge ReAct（LLM 账单随并发线性放大）
		MaxConcurrent: 3,
	})

	// 5. 创建 PlannerAgent
	intelligence := planner.NewIntelligence(h.router, h.logger)

	plannerAgent := planner.NewAgent(planner.AgentConfig{
		TaskID:        taskID,
		Graph:         h.graph,
		Planner:       intelligence,
		EventBus:      h.eventBus,
		Logger:        h.logger.With().Str("component", "planner_agent").Logger(),
		FunctionTools: plannerCfg.FunctionTools,
	})

	// 6. 创建 MonitorAgent
	provider, err := h.router.For(ctx, "medium")
	if err != nil {
		return executor.Report{}, fmt.Errorf("failed to get provider for monitor: %w", err)
	}

	monitorAgent := monitor.New(monitor.Config{
		TaskID:        taskID,
		FunctionTools: monitorCfg.FunctionTools,
		Graph:         h.graph,
		EventBus:      h.eventBus,
		Provider:      provider,
		Interval:      6 * time.Minute,
		Logger:        h.logger.With().Str("component", "monitor_agent").Logger(),
		Checkpointer:  h.checkpointer,
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
