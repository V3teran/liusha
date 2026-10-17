package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/cognition"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/monitor"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/google/uuid"
	"github.com/rs/zerolog"
)

// runCognition drives a single assignment through the L4 cognition loop:
// four independent agents (Planner, Executor, Evaluator, Monitor) coordinate via bus.Bus.
// sb 是本任务独占的沙箱客户端；nil 表示无沙箱（run_command/drive_browser 不注册）。
func (h handler) runCognition(
	ctx context.Context,
	taskID, host, brief string,
	sb sandbox.Client,
) (executor.Report, error) {
	h.logger.Info().
		Str("task_id", taskID).
		Str("brief", brief).
		Msg("[RUN_COGNITION] 启动四Agent架构")

	if h.graph == nil || taskID == "" || h.eventBus == nil {
		return executor.Report{}, fmt.Errorf("graph and eventBus are required")
	}

	if err := h.ensureRootObjective(ctx, taskID, brief); err != nil {
		return executor.Report{}, err
	}

	stack, err := h.assembleStack(ctx, taskID, host, brief, sb)
	if err != nil {
		return executor.Report{}, err
	}

	// 启动四个 Agent（异步；启停封装成 agentLifecycle 供控制平面 pause/resume 复用）
	lifecycle := newAgentLifecycle(ctx, h.logger, taskID, []agentSpec{
		{name: "planner", start: stack.plannerAgent.Run},
		{name: "executor", start: stack.executorAgent.Run},
		{name: "evaluator", start: stack.evaluatorAgent.Run},
		{name: "monitor", start: stack.monitorAgent.Run},
	})

	lifecycle.Start()
	defer lifecycle.Stop()

	// 任务结束浏览器清理：本任务身份前缀的 daemon/chromium 逐个优雅
	// reset——共享容器下立即释放内存（不等容器空闲回收），且只动本任务的身份
	// （并发任务的其他身份不受影响）。best-effort，失败仅告警。
	defer func() {
		if script := tools.BrowserCleanupScript(taskID); script != "" && sb != nil {
			cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			if res, err := sb.Exec(cctx, sandbox.ExecRequest{
				TaskID: taskID, AgentID: "cleanup", Command: script, TimeoutSeconds: 30,
			}); err != nil {
				h.logger.Warn().Err(err).Str("task_id", taskID).Msg("浏览器清理执行失败（best-effort）")
			} else {
				h.logger.Info().Str("task_id", taskID).Int("exit_code", res.ExitCode).Msg("任务结束浏览器身份已清理")
			}
		}
	}()

	// 控制平面消费者：轮询 task_control_event（pause/resume/terminate/adjust_goal/inject）
	if h.controlPlane != nil {
		stopConsumer := startControlConsumer(ctx, taskID, h.controlPlane, h.graph, stack.detector, lifecycle, h.logger)
		defer stopConsumer()
	}

	h.logger.Info().Str("task_id", taskID).Msg("四个 Agent 已启动，等待任务完成")

	// 阻塞等待完成检测，随后停止所有 Agent（浏览器清理 defer 依赖 agents 已停）
	result := stack.detector.Start(ctx)
	lifecycle.Stop()

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

// ensureRootObjective 创建任务根 Objective 节点（幂等：onboard 已建则跳过，防双根）。
func (h handler) ensureRootObjective(ctx context.Context, taskID, brief string) error {
	if brief == "" {
		h.logger.Warn().
			Str("task_id", taskID).
			Msg("任务 brief 为空，跳过创建根 Objective")
		return nil
	}
	if existing, listErr := h.graph.ListNodesByKind(ctx, taskID, core.KindObjective); listErr == nil && len(existing) > 0 {
		h.logger.Info().
			Str("task_id", taskID).
			Int("existing_objectives", len(existing)).
			Msg("根 Objective 已存在（onboard 创建），跳过重复创建")
		return nil
	}

	contentJSON, _ := json.Marshal(map[string]string{"brief": brief})
	// 数据库约束：objective 类型节点必须有非空 state
	initialState := explorationgraph.StateOpen

	createdID, err := h.graph.CreateNode(ctx, explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindObjective,
		Content:    contentJSON,
		State:      &initialState,
		Priority:   explorationgraph.PriorityHigh,
		SourceType: explorationgraph.SourceUser,
		SourceID:   "task_init",
	})
	if err != nil {
		return fmt.Errorf("failed to create root objective: %w", err)
	}
	h.logger.Info().
		Str("task_id", taskID).
		Str("root_id", createdID).
		Str("brief", brief).
		Msg("已创建根 Objective 节点")
	return nil
}

// agentConfigs 是四 Agent 的 DB 配置（function_tools/cli_tools/skills 白名单的事实源）。
type agentConfigs struct {
	executor  agent.Agent
	planner   agent.Agent
	evaluator agent.Agent
	monitor   agent.Agent
}

// loadAgentConfigs 经 cfgStore 三级缓存读四 Agent 配置；读失败降级为空配置。
// 前端改配置 → UpdateAgent 失效广播 → 本进程 L1 清 → 本任务即读新值。
func (h handler) loadAgentConfigs(ctx context.Context) agentConfigs {
	getCfg := func(code string) agent.Agent {
		cfg, err := h.cfgStore.AgentByCode(ctx, code)
		if err != nil {
			h.logger.Warn().Err(err).Str("code", code).Msg("加载 agent 配置失败，使用空配置")
			return agent.Agent{Code: code}
		}
		return cfg
	}
	return agentConfigs{
		executor:  getCfg(constants.AgentCodeExecutor),
		planner:   getCfg(constants.AgentCodePlanner),
		evaluator: getCfg(constants.AgentCodeEvaluator),
		monitor:   getCfg(constants.AgentCodeMonitor),
	}
}

// cognitionStack 是一次认知循环装配的产物：四 Agent 与完成检测器。
type cognitionStack struct {
	detector       *cognition.CompletionDetector
	executorAgent  *executor.Agent
	evaluatorAgent *evaluator.Agent
	plannerAgent   *planner.Agent
	monitorAgent   *monitor.Agent
}

// assembleStack 装配四 Agent：配置 → 工具栈 → 各 Agent 构造器。装配是一次
// 线性依赖链，按 Agent 分 constructors，各自独立可读。
func (h handler) assembleStack(
	ctx context.Context,
	taskID, host, brief string,
	sb sandbox.Client,
) (*cognitionStack, error) {
	cfgs := h.loadAgentConfigs(ctx)

	h.logger.Info().
		Strs("cli_tools", cfgs.executor.CliTools).
		Int("cli_tools_count", len(cfgs.executor.CliTools)).
		Strs("skills", cfgs.executor.Skills).
		Msg("Executor Agent 配置已加载")

	// skill 渐进式加载视图：按各 agent 的 skills 声明从 DB 全集切白名单——
	// Tier 1（frontmatter 索引进 system prompt）与 Tier 2（read_skill 正文）
	// 共用同一视图，声明什么暴露什么（nil=未装配，DB 声明为空同样不装配）。
	executorSkillView := skillView(h.skills, cfgs.executor.Skills)
	evaluatorSkillView := skillView(h.skills, cfgs.evaluator.Skills)

	// LLM 审计归属：tool_invocation.agent_run_id 外键指向 agent_run.id，
	// 取本 task 的 run 行（api expandItem 建的那条）；查不到留空 → NULL。
	agentRunID := ""
	if runs, rErr := h.agentRuns.ListByTask(ctx, taskID, 1); rErr == nil && len(runs) > 0 {
		agentRunID = runs[0].ID
	} else if rErr != nil {
		h.logger.Warn().Err(rErr).Str("task_id", taskID).Msg("查询 agent_run 失败，tool_invocation 将不关联 run")
	}

	// 工具注册表 + 遥测 interceptor：每次工具调用落 tool_invocation 并节流续命
	// task.heartbeat_at（reaper 判活依据）。跨包工具依赖（executor 注册与
	// evaluator BuildTools 共用同一份，避免字面量漂移）。
	activities := newActivityTracker()
	reg := registry.New()
	reg.AddInterceptor(h.toolRecordInterceptor(agentRunID, taskID, activities))
	toolDeps := func(skillsView skill.Reader) tools.Deps {
		return tools.Deps{
			TaskID:     taskID,
			AgentRunID: agentRunID,
			Host:       host,
			Tasks:      h.tasks,
			Findings:   h.findings,
			Corpus:     h.corpus,
			Embedder:   h.embedder,
			Reranker:   h.reranker,
			Insights:   h.insights,
			ProxyStore: h.proxyStore,
			AgentStore: h.agentStore,
			Creds:      h.creds,
			Sandbox:    sb,         // run_command/drive_browser 依赖；nil 时这两个工具不注册
			Skills:     skillsView, // read_skill 依赖；声明为空时不注册
		}
	}
	tools.RegisterAll(reg, toolDeps(executorSkillView))

	// CLI 工具清单按白名单过滤
	filteredManifest := h.filterManifest(cfgs.executor.CliTools)

	// executor：Engine（ReAct）→ Coordinator → Agent
	executorAgent := h.buildExecutorAgent(ctx, taskID, host, brief, agentRunID, cfgs.executor,
		filteredManifest, executorSkillView, executor.NewRegistry(reg))

	// evaluator：裁决官（ReAct judge）→ 复现晋升门 → Agent
	evaluatorAgent := h.buildEvaluatorAgent(ctx, taskID, host, agentRunID, cfgs.evaluator,
		evaluatorSkillView, toolDeps)

	// planner
	intelligence := planner.NewIntelligence(h.router, h.logger).
		WithSystemPrompt(cfgs.planner.SystemPrompt).
		WithComplexity(cfgs.planner.Complexity).
		WithMaxIterations(cfgs.planner.MaxIterations)
	plannerAgent := planner.NewAgent(planner.AgentConfig{
		TaskID:        taskID,
		AgentRunID:    agentRunID,
		Graph:         h.graph,
		Planner:       intelligence,
		EventBus:      h.eventBus,
		Logger:        h.logger.With().Str("component", "planner_agent").Logger(),
		FunctionTools: cfgs.planner.FunctionTools,
	})

	// monitor（LLM 档位 = agent.complexity，文档 complexity 种子经三级缓存读）
	provider, err := h.router.For(ctx, llm.ParseComplexity(cfgs.monitor.Complexity))
	if err != nil {
		return nil, fmt.Errorf("failed to get provider for monitor: %w", err)
	}
	monitorAgent := monitor.New(monitor.Config{
		TaskID:        taskID,
		AgentRunID:    agentRunID,
		FunctionTools: cfgs.monitor.FunctionTools,
		SystemPrompt:  cfgs.monitor.SystemPrompt,
		MaxIterations: cfgs.monitor.MaxIterations,
		Graph:         h.graph,
		EventBus:      h.eventBus,
		Provider:      provider,
		Interval:      constants.MonitorInterval,
		Activity:      activities.idleFor,
		Logger:        h.logger.With().Str("component", "monitor_agent").Logger(),
		Checkpointer:  h.checkpointer,
	})

	detector := cognition.NewCompletionDetector(cognition.Config{
		TaskID:        taskID,
		Graph:         h.graph,
		Bus:           h.eventBus,
		Logger:        h.logger,
		MaxSteps:      h.runnerCfg.MaxStepsPerTask, // 0 表示无限制
		CheckInterval: 5 * time.Second,
	})

	return &cognitionStack{
		detector:       detector,
		executorAgent:  executorAgent,
		evaluatorAgent: evaluatorAgent,
		plannerAgent:   plannerAgent,
		monitorAgent:   monitorAgent,
	}, nil
}

// filterManifest 按 cli_tools 白名单过滤工具清单（nil/空清单 → 空集并告警）。
func (h handler) filterManifest(cliTools []string) *manifest.Manifest {
	if h.toolsManifest == nil || len(cliTools) == 0 {
		h.logger.Warn().Msg("未配置 cli_tools 或 tools.yaml 未加载，外部工具不可用")
		return &manifest.Manifest{Tools: []manifest.Tool{}}
	}
	filtered := h.toolsManifest.FilterByNames(cliTools)
	h.logger.Info().
		Int("total_in_yaml", len(h.toolsManifest.Tools)).
		Int("whitelisted", len(cliTools)).
		Int("filtered_result", len(filtered.Tools)).
		Strs("tool_names", filtered.Names()).
		Msg("CLI 工具已过滤（白名单机制）")
	return filtered
}

// buildExecutorAgent 组装 Engine（ReAct）→ Coordinator → Executor Agent。
func (h handler) buildExecutorAgent(
	ctx context.Context,
	taskID, host, brief, agentRunID string,
	cfg agent.Agent,
	filteredManifest *manifest.Manifest,
	skillsView skill.Reader,
	execRegistry *executor.Registry,
) *executor.Agent {
	traceCollector := executor.NewTraceCollector(h.agentStore, h.logger)
	engine := executor.NewEngine(executor.EngineConfig{
		Router:         h.router,
		Findings:       h.findings,
		Graph:          h.graph,    // observation 落图与 kill 检查依赖；nil 会在产出观察时空指针崩溃
		AgentRunID:     agentRunID, // LLM 审计归属本轮 run
		Registry:       execRegistry,
		FunctionTools:  cfg.FunctionTools,                                 // function_tools 白名单（nil=全量）
		ToolsManifest:  filteredManifest,                                  // CLI 工具清单（白名单过滤后）
		Skills:         skillCards(ctx, skillsView, h.logger, "executor"), // Tier 1 skill 索引（正文按需 read_skill）
		SystemPrompt:   cfg.SystemPrompt,                                  // 角色章程（agent.system_prompt，前端可调）
		Complexity:     cfg.Complexity,                                    // LLM 档位（agent.complexity，文档 complexity 种子）
		MaxIterations:  cfg.MaxIterations,                                 // ReAct 迭代上限（0=不设限）
		Brief:          brief,                                             // 任务简报逐字进 executor system prompt（入口锚定）
		Checkpointer:   h.checkpointer,
		TraceCollector: traceCollector, // 从 agent_traffic 构建完整执行轨迹
		Logger:         h.logger,
	})
	coord := executor.NewCoordinatorWithEngine(taskID, host, h.findings, engine, h.logger)

	return executor.NewAgent(executor.AgentConfig{
		TaskID:       taskID,
		Graph:        h.graph,
		Executor:     coord,
		EventBus:     h.eventBus,
		Logger:       h.logger.With().Str("component", "executor_agent").Logger(),
		Checkpointer: h.checkpointer,
		MaxParallel:  4, // 无依赖 action 并发执行（DAG 定可并行性；浏览器已按 action 隔离 tab）
	})
}

// buildEvaluatorAgent 组装裁决官（ReAct judge）→ 复现晋升门 → Evaluator Agent。
// Replayer 零依赖（复现配方自包含完整 HTTP 请求，不从 traffic 表取流量——工具自发
// 流量同样可复现）。凭据池按 host 注入会话上下文：LLM 漏写 Cookie 的配方也能以
// 登录态重放，防真实漏洞因重放落到登录页而被机器护栏误证伪。
func (h handler) buildEvaluatorAgent(
	ctx context.Context,
	taskID, host, agentRunID string,
	cfg agent.Agent,
	skillsView skill.Reader,
	toolDeps func(skill.Reader) tools.Deps,
) *evaluator.Agent {
	var evaluatorCLImanifest *manifest.Manifest
	if h.toolsManifest != nil && len(cfg.CliTools) > 0 {
		evaluatorCLImanifest = h.toolsManifest.FilterByNames(cfg.CliTools)
	}
	judge := evaluator.NewRouterJudge(h.router, h.logger).
		WithFunctionTools(cfg.FunctionTools).
		WithCLIManifest(evaluatorCLImanifest).
		WithSkills(skillCards(ctx, skillsView, h.logger, "evaluator")).
		WithSystemPrompt(cfg.SystemPrompt).
		WithComplexity(cfg.Complexity).
		WithMaxIterations(cfg.MaxIterations).
		WithExtraTools(tools.BuildTools(toolDeps(skillsView), cfg.FunctionTools))

	// 复现晋升门：域交付管道（finding.ClaimSink）与指纹归一（finding 形状）
	// 由领域包注入——通用门对领域零感知
	promoter := evaluator.New(h.graph, executor.NewReplayer().WithCredentials(h.creds, host),
		finding.NewClaimSink(h.findings, agentRunID)).
		WithJudge(judge). // ReAct 裁决官：replay_for_verification + CLI 复核工具
		WithFingerprintCodec(finding.FingerprintNormalizer{}).
		WithLogger(h.logger)

	return evaluator.NewAgent(evaluator.AgentConfig{
		TaskID:     taskID,
		AgentRunID: agentRunID,
		Evaluator:  promoter,
		EventBus:   h.eventBus,
		Graph:      h.graph,
		Logger:     h.logger.With().Str("component", "evaluator_agent").Logger(),
		// 验证并发：每个验证 = 机器复放 + 一轮 judge ReAct（LLM 账单随并发线性放大）
		// 降低到 3 避免 GLM API 限流
		MaxConcurrent: 3,
		PollInterval:  5 * time.Second, // 每 5 秒轮询一次未验证的 observation
	})
}

// skillView 按 agent.skills 声明从 DB 全集建白名单视图（渐进式加载两层共用）。
// 后端未装配或声明为空 → 返回 nil 接口（read_skill 不注册、Tier 1 不渲染）——
// 语义与 function_tools/cli_tools 白名单一致：声明什么装配什么。
func skillView(src skill.Reader, declared []string) skill.Reader {
	if src == nil || len(declared) == 0 {
		return nil
	}
	return skill.Allow(src, declared)
}

// skillCards 取视图的 Tier 1 索引（frontmatter 列表，经多级缓存读），
// 供 engine/judge 渲染进 system prompt。读失败告警并降级为空索引
// （任务不因此中断，但 LLM 将没有技能目录——配置链路需检查）。
func skillCards(ctx context.Context, r skill.Reader, logger zerolog.Logger, agentCode string) []*skill.Card {
	if r == nil {
		return nil
	}
	cards, err := r.Metas(ctx)
	if err != nil {
		logger.Warn().Err(err).Str("agent", agentCode).
			Msg("skill 索引读取失败，本任务技能目录为空（read_skill 单条读可能仍可用）")
		return nil
	}
	return cards
}
