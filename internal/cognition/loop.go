// loop.go 认知循环的装配与驱动：四 Agent 组装、控制平面消费、完成检测。
// （CompletionDetector 在 completion.go；生命周期/控制平面/活动打点/工具遥测
// 分别见 lifecycle.go / control.go / activity.go / telemetry.go。）

package cognition

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/agentrun"
	"github.com/V3teran/liusha/internal/bus"
	configstore "github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/controlplane"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/monitor"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
)

// Deps 是一次认知循环的全部外部依赖（由 runner 装配注入；全部字段只读）。
// 显式依赖结构让循环装配可以脱离 asynq/handler 单测（白名单/视图/构造链
// 是历史 bug 高发区）。
type Deps struct {
	Graph        *explorationgraph.Store
	EventBus     bus.EventBus
	ControlPlane *controlplane.Store
	CfgStore     *configstore.Store
	Router       *fwllm.Router
	Logger       zerolog.Logger

	Skills        *skill.StoreReader
	ToolsManifest *manifest.ToolManifest
	Creds         credential.Provider

	// 遥测后端（toolRecordInterceptor 用）
	ToolCalls *toolinvocation.Store
	Tasks     *task.Store

	// 工具域存储
	AgentRuns  *agentrun.Store
	Findings   *finding.Store
	Corpus     *corpus.Store
	Embedder   corpus.Embedder
	Reranker   corpus.Reranker
	Insights   *insight.Store
	ProxyStore *traffic.ProxyStore
	AgentStore *traffic.AgentStore

	MaxStepsPerTask int // 0 = 完成检测器步数无限制

	// Windows 是各角色 ReAct 滚动窗口条数（DB compaction 组在任务开始时的快照，
	// 装配层现读传入；0 值由各角色使用点回退内置默认）。
	Windows MessageWindows

	// ToolLimits 是 run_command 的超时钳制上限与输出尾部保留字节（DB runtime 组
	// 任务开始时快照；0 值由工具层回退内置默认）。
	ToolLimits ToolLimits

	// 会话思考流（四 agent 推理实时进时间线）：两者齐备且 conversationID 非空才启用。
	Conversations  *conversation.Store
	EventPublisher *convstream.Publisher
}

// MessageWindows 是各角色 ReAct 滚动窗口条数（系统配置 compaction 组快照）。
type MessageWindows struct {
	Planner   int
	Executor  int
	Evaluator int
	Monitor   int
}

// ToolLimits 是 run_command 的运行时限制（系统配置 runtime 组快照）。
type ToolLimits struct {
	StepToolTimeoutSeconds int // run_command 超时钳制上限秒（LLM 自传 timeout_seconds 的上界）
	RunTailBytes           int // stdout/stderr 保留尾部字节数
}

// Run 驱动一次任务的认知循环：四个平级 Agent（planner/executor/evaluator/monitor）
// 经 bus 广播协作，探索图为共享状态面，CompletionDetector 判定收敛后返回报告。
// sb 是本任务独占的沙箱客户端；nil 表示无沙箱（run_command/drive_browser 不注册）。
func Run(ctx context.Context, d *Deps, taskID, host, brief, conversationID string, sb sandbox.Client) (executor.Report, error) {
	return d.run(ctx, taskID, host, brief, conversationID, sb)
}

// run 是 Run 的内部实现（拆一层让 Run 的签名稳定、内部演进不惊动调用方）。
func (d *Deps) run(
	ctx context.Context,
	taskID, host, brief, conversationID string,
	sb sandbox.Client,
) (executor.Report, error) {
	d.Logger.Info().
		Str("brief", brief).
		Msg("[RUN_COGNITION] 启动四Agent架构")

	if d.Graph == nil || taskID == "" || d.EventBus == nil {
		return executor.Report{}, fmt.Errorf("graph and eventBus are required")
	}

	if err := d.ensureRootObjective(ctx, taskID, brief); err != nil {
		return executor.Report{}, err
	}

	stack, err := d.assembleStack(ctx, taskID, host, brief, conversationID, sb)
	if err != nil {
		return executor.Report{}, err
	}
	// 思考流随循环关闭：冲刷未发增量（终态消息由各 agent 的 Done 落）。
	defer stack.thoughts.Close()

	// 启动四个 Agent（异步；启停封装成 agentLifecycle 供控制平面 pause/resume 复用）
	lifecycle := newAgentLifecycle(ctx, d.Logger, []agentSpec{
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
				d.Logger.Warn().Err(err).Msg("浏览器清理执行失败（best-effort）")
			} else {
				d.Logger.Info().Int("exit_code", res.ExitCode).Msg("任务结束浏览器身份已清理")
			}
		}
	}()

	// 控制平面消费者：轮询 task_control_event（pause/resume/terminate/adjust_goal/inject）
	if d.ControlPlane != nil {
		stopConsumer := startControlConsumer(ctx, taskID, d.ControlPlane, d.Graph, stack.detector, lifecycle, d.Logger)
		defer stopConsumer()
	}

	d.Logger.Info().Msg("四个 Agent 已启动，等待任务完成")

	// 阻塞等待完成检测，随后停止所有 Agent（浏览器清理 defer 依赖 agents 已停）
	result := stack.detector.Start(ctx)
	lifecycle.Stop()

	// 根目标终态：任务收敛/终止时关闭 objective（此前永远停在 open，图生命
	// 周期缺终态语义）。被终止的任务标 aborted，其余标 done。best-effort。
	d.closeObjectives(ctx, taskID, result.StopReason)

	d.Logger.Info().
		Int64("duration_ms", result.Duration.Milliseconds()).
		Int("steps", result.Steps).
		Int("promoted", result.Promoted).
		Str("stop_reason", result.StopReason).
		Msg("认知循环完成")

	return executor.Report{
		Steps:      result.Steps,
		Promoted:   result.Promoted,
		Attempts:   result.Attempts,
		StopReason: result.StopReason,
	}, nil
}

// closeObjectives 把任务的全部 open 目标节点置终态（done；被终止的任务
// aborted）——含根目标与 result 派生的次级目标，图生命周期对全部 objective
// 有始有终。已终态的目标不动（aborted 护栏另在状态迁移层兜底）。best-effort。
func (d *Deps) closeObjectives(ctx context.Context, taskID, stopWhy string) {
	if d.Graph == nil {
		return
	}
	objectives, err := d.Graph.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil || len(objectives) == 0 {
		return
	}
	terminal := explorationgraph.StateCompleted
	if strings.Contains(stopWhy, "terminate") || strings.Contains(stopWhy, "cancel") {
		terminal = explorationgraph.StateAborted
	}
	for _, obj := range objectives {
		if obj.State == nil || (*obj.State != explorationgraph.StateOpen && *obj.State != explorationgraph.StateRunning) {
			continue // 已终态（done/failed/aborted）不重复迁移
		}
		if err := d.Graph.TransitionState(ctx, obj.ID, terminal, nil); err != nil {
			d.Logger.Warn().Err(err).Str("objective_id", obj.ID).
				Msg("目标终态回写失败（该 objective 保持 open）")
		}
	}
}

// ensureRootObjective 创建任务根 Objective 节点（幂等：onboard 已建则跳过，防双根）。
func (d *Deps) ensureRootObjective(ctx context.Context, taskID, brief string) error {
	if brief == "" {
		d.Logger.Warn().
			Msg("任务 brief 为空，跳过创建根 Objective")
		return nil
	}
	if existing, listErr := d.Graph.ListNodesByKind(ctx, taskID, core.KindObjective); listErr == nil && len(existing) > 0 {
		d.Logger.Info().
			Int("existing_objectives", len(existing)).
			Msg("根 Objective 已存在（onboard 创建），跳过重复创建")
		return nil
	}

	// objective.content 的读契约是 description（GetObjective / planner 规划上下文都读它）
	contentJSON, _ := json.Marshal(map[string]string{"description": brief})
	// 数据库约束：objective 类型节点必须有非空 state
	initialState := explorationgraph.StateOpen

	createdID, err := d.Graph.CreateNode(ctx, explorationgraph.Node{
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
	d.Logger.Info().
		Str("root_id", createdID).
		Str("brief", brief).
		Msg("已创建根 Objective 节点")
	return nil
}

// agentConfigs 是四 Agent 的 DB 配置（function_tools/cli_tools/skills 白名单的事实源）。
type agentConfigs struct {
	executor  agent.Profile
	planner   agent.Profile
	evaluator agent.Profile
	monitor   agent.Profile
}

// loadAgentConfigs 经 cfgStore 三级缓存读四 Agent 配置；读失败降级为空配置。
// 前端改配置 → UpdateAgent 失效广播 → 本进程 L1 清 → 本任务即读新值。
func (d *Deps) loadAgentConfigs(ctx context.Context) agentConfigs {
	getCfg := func(code string) agent.Profile {
		cfg, err := d.CfgStore.AgentByCode(ctx, code)
		if err != nil {
			d.Logger.Warn().Err(err).Str("code", code).Msg("加载 agent 配置失败，使用空配置")
			return agent.Profile{Code: code}
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
	thoughts       *reasoning.Stream // 会话思考流（Close 冲刷残留增量）
	detector       *CompletionDetector
	executorAgent  *executor.Agent
	evaluatorAgent *evaluator.Agent
	plannerAgent   *planner.Agent
	monitorAgent   *monitor.Agent
}

// assembleStack 装配四 Agent：配置 → 工具栈 → 各 Agent 构造器。装配是一次
// 线性依赖链，按 Agent 分 constructors，各自独立可读。
func (d *Deps) assembleStack(
	ctx context.Context,
	taskID, host, brief, conversationID string,
	sb sandbox.Client,
) (*cognitionStack, error) {
	cfgs := d.loadAgentConfigs(ctx)

	// 会话思考流：四 agent 的推理增量实时进时间线（无会话时 no-op）。
	thoughts := reasoning.NewStream(d.EventPublisher, d.Conversations, conversationID, d.Logger)

	d.Logger.Info().
		Strs("cli_tools", cfgs.executor.CliTools).
		Int("cli_tools_count", len(cfgs.executor.CliTools)).
		Strs("skills", cfgs.executor.Skills).
		Msg("Executor Agent 配置已加载")

	// skill 渐进式加载视图：按各 agent 的 skills 声明从 DB 全集切白名单——
	// Tier 1（frontmatter 索引进 system prompt）与 Tier 2（read_skill 正文）
	// 共用同一视图，声明什么暴露什么（nil=未装配，DB 声明为空同样不装配）。
	executorSkillView := skillView(d.Skills, cfgs.executor.Skills)
	evaluatorSkillView := skillView(d.Skills, cfgs.evaluator.Skills)

	// LLM 审计归属：tool_invocation.agent_run_id 外键指向 agent_run.id，
	// 取本 task 的 run 行（api expandItem 建的那条）；查不到留空 → NULL。
	agentRunID := ""
	if runs, rErr := d.AgentRuns.ListByTask(ctx, taskID, 1); rErr == nil && len(runs) > 0 {
		agentRunID = runs[0].ID
	} else if rErr != nil {
		d.Logger.Warn().Err(rErr).Msg("查询 agent_run 失败，tool_invocation 将不关联 run")
	}

	// 工具注册表 + 拦截器链：遥测（tool_invocation 落库 + 节流续命）与时间线
	// （工具过程事件进会话）各一个拦截器，单一职责。跨包工具依赖（executor 注册
	// 与 evaluator BuildTools 共用同一份，避免字面量漂移）。
	activities := newActivityTracker()
	reg := registry.New()
	reg.AddInterceptor(d.toolRecordInterceptor(agentRunID, taskID, activities))
	reg.AddInterceptor(d.toolTimelineInterceptor(conversationID))
	toolDeps := func(skillsView skill.Reader) tools.Deps {
		return tools.Deps{
			TaskID:     taskID,
			AgentRunID: agentRunID,
			Host:       host,
			Logger:     d.Logger,
			Tasks:      d.Tasks,
			Corpus:     d.Corpus,
			Embedder:   d.Embedder,
			Reranker:   d.Reranker,
			Insights:   d.Insights,
			ProxyStore: d.ProxyStore,
			AgentStore: d.AgentStore,
			Creds:      d.Creds,
			Sandbox:    sb,         // run_command/drive_browser 依赖；nil 时这两个工具不注册
			Skills:     skillsView, // read_skill 依赖；声明为空时不注册

			// run_command 超时钳制与输出尾部保留（系统配置 runtime 组快照）
			StepToolTimeoutSeconds: d.ToolLimits.StepToolTimeoutSeconds,
			RunTailBytes:           d.ToolLimits.RunTailBytes,
		}
	}
	tools.RegisterAll(reg, toolDeps(executorSkillView))

	// CLI 工具清单按白名单过滤
	filteredManifest := d.filterManifest(cfgs.executor.CliTools)

	// executor：Engine（ReAct）→ Coordinator → Agent
	executorAgent := d.buildExecutorAgent(ctx, taskID, host, brief, agentRunID, cfgs.executor,
		filteredManifest, executorSkillView, executor.NewRegistry(reg), thoughts.TapFor("executor"))

	// evaluator：裁决官（ReAct judge）→ 复现晋升门 → Agent
	evaluatorAgent := d.buildEvaluatorAgent(ctx, taskID, host, agentRunID, conversationID, cfgs.evaluator,
		evaluatorSkillView, toolDeps, reg, thoughts.TapFor("evaluator"))

	// planner。黑板摘要：assignment 内 insight 按写入序取顶并格式化——
	// 规划者消费执行者经验（黑板单向是审计确认的断裂：executor 写、只有
	// executor 读）。经 gatherContext 注入而非注册工具：决策变量显式喂给
	// 决策者，不指望模型自己想起来查。
	plannerDigest := func(ctx context.Context) []string {
		if d.Insights == nil || d.Tasks == nil {
			return nil
		}
		tk, err := d.Tasks.GetByID(ctx, taskID)
		if err != nil {
			return nil
		}
		entries, err := d.Insights.List(ctx, tk.AssignmentID, 20)
		if err != nil {
			return nil
		}
		lines := make([]string, 0, len(entries))
		for _, e := range entries {
			lines = append(lines, fmt.Sprintf("[%s/%s/%s] %s", e.Category, e.Priority, e.Confidence, e.Summary))
		}
		return lines
	}
	engine := planner.NewEngine(d.Router, d.Logger).
		WithSystemPrompt(cfgs.planner.SystemPrompt).
		WithComplexity(cfgs.planner.Complexity).
		WithMaxIterations(cfgs.planner.MaxIterations).
		WithAgentRoster([]planner.AgentBrief{
			// 团队名册：planner 规划时读取各角色调度摘要（agent.description，前端可编辑），
			// 让动作指令对准执行方职责边界。
			{Code: constants.AgentCodePlanner, Name: cfgs.planner.Name, Description: cfgs.planner.Description},
			{Code: constants.AgentCodeExecutor, Name: cfgs.executor.Name, Description: cfgs.executor.Description},
			{Code: constants.AgentCodeEvaluator, Name: cfgs.evaluator.Name, Description: cfgs.evaluator.Description},
			{Code: constants.AgentCodeMonitor, Name: cfgs.monitor.Name, Description: cfgs.monitor.Description},
		}).
		WithInsightsDigest(plannerDigest).
		WithMessageWindow(d.Windows.Planner).
		WithThoughts(thoughts.TapFor("planner")).
		WithToolWrapper(reg.WrapTool) // 图读工具入共享审计链（遥测/时间线）
	plannerAgent := planner.NewAgent(planner.AgentConfig{
		TaskID:        taskID,
		AgentRunID:    agentRunID,
		Graph:         d.Graph,
		Planner:       engine,
		EventBus:      d.EventBus,
		Logger:        d.Logger.With().Str("component", "planner_agent").Logger(),
		FunctionTools: cfgs.planner.FunctionTools,
	})

	// monitor（LLM 档位 = agent.complexity；provider 每次评估现取，见 monitor.evaluate）
	monitorAgent := monitor.New(monitor.Config{
		MessageWindow: d.Windows.Monitor,
		Thoughts:      thoughts.TapFor("monitor"),
		TaskID:        taskID,
		AgentRunID:    agentRunID,
		FunctionTools: cfgs.monitor.FunctionTools,
		SystemPrompt:  cfgs.monitor.SystemPrompt,
		MaxIterations: cfgs.monitor.MaxIterations,
		Graph:         d.Graph,
		EventBus:      d.EventBus,
		Router:        d.Router,
		Complexity:    cfgs.monitor.Complexity,
		Interval:      constants.MonitorInterval,
		Activity:      activities.idleFor,
		WrapTool:      reg.WrapTool, // 监察工具入共享审计链（遥测/时间线）
		Logger:        d.Logger.With().Str("component", "monitor_agent").Logger(),
	})

	detector := NewCompletionDetector(Config{
		TaskID:        taskID,
		Graph:         d.Graph,
		Bus:           d.EventBus,
		Logger:        d.Logger,
		MaxSteps:      d.MaxStepsPerTask, // 0 表示无限制
		CheckInterval: 5 * time.Second,
	})

	return &cognitionStack{
		thoughts:       thoughts,
		detector:       detector,
		executorAgent:  executorAgent,
		evaluatorAgent: evaluatorAgent,
		plannerAgent:   plannerAgent,
		monitorAgent:   monitorAgent,
	}, nil
}

// filterManifest 按 cli_tools 白名单过滤工具清单（nil/空清单 → 空集并告警）。
func (d *Deps) filterManifest(cliTools []string) *manifest.ToolManifest {
	if d.ToolsManifest == nil || len(cliTools) == 0 {
		d.Logger.Warn().Msg("未配置 cli_tools 或 tools.yaml 未加载，外部工具不可用")
		return &manifest.ToolManifest{Tools: []manifest.Tool{}}
	}
	filtered := d.ToolsManifest.FilterByNames(cliTools)
	d.Logger.Info().
		Int("total_in_yaml", len(d.ToolsManifest.Tools)).
		Int("whitelisted", len(cliTools)).
		Int("filtered_result", len(filtered.Tools)).
		Strs("tool_names", filtered.Names()).
		Msg("CLI 工具已过滤（白名单机制）")
	return filtered
}

// buildExecutorAgent 组装 Engine（ReAct）→ Coordinator → Executor Agent。
func (d *Deps) buildExecutorAgent(
	ctx context.Context,
	taskID, host, brief, agentRunID string,
	cfg agent.Profile,
	filteredManifest *manifest.ToolManifest,
	skillsView skill.Reader,
	execRegistry *executor.Registry,
	thoughts reasoning.Tap,
) *executor.Agent {
	traceCollector := executor.NewTraceCollector(d.AgentStore, d.Logger)
	engine := executor.NewEngine(executor.EngineConfig{
		Router:         d.Router,
		Graph:          d.Graph,    // observation 落图与 kill 检查依赖；nil 会在产出观察时空指针崩溃
		AgentRunID:     agentRunID, // LLM 审计归属本轮 run
		Registry:       execRegistry,
		FunctionTools:  cfg.FunctionTools,                                 // function_tools 白名单（nil=拒绝，["*"]=全量）
		ToolsManifest:  filteredManifest,                                  // CLI 工具清单（白名单过滤后）
		Skills:         skillCards(ctx, skillsView, d.Logger, "executor"), // Tier 1 skill 索引（正文按需 read_skill）
		SystemPrompt:   cfg.SystemPrompt,                                  // 角色章程（agent.system_prompt，前端可调）
		Complexity:     cfg.Complexity,                                    // LLM 档位（agent.complexity，文档 complexity 种子）
		MaxIterations:  cfg.MaxIterations,                                 // ReAct 迭代上限（0=不设限）
		ModifierWindow: d.Windows.Executor,                                // ReAct 滚动窗口条数（系统配置 compaction 组）
		Brief:          brief,                                             // 任务简报逐字进 executor system prompt（入口锚定）
		TraceCollector: traceCollector,                                    // 从 agent_traffic 构建完整执行轨迹
		Thoughts:       thoughts,                                          // 思考流（nil=不流式）
		Logger:         d.Logger,
	})
	coord := executor.NewCoordinatorWithEngine(taskID, host, engine, d.Logger)

	return executor.NewAgent(executor.AgentConfig{
		TaskID:      taskID,
		Graph:       d.Graph,
		Executor:    coord,
		EventBus:    d.EventBus,
		Logger:      d.Logger.With().Str("component", "executor_agent").Logger(),
		MaxParallel: 4, // 无依赖 action 并发执行（DAG 定可并行性；浏览器已按 action 隔离 tab）
	})
}

// buildEvaluatorAgent 组装裁决官（ReAct judge）→ 复现晋升门 → Evaluator Agent。
// Replayer 零依赖（复现配方自包含完整 HTTP 请求，不从 traffic 表取流量——工具自发
// 流量同样可复现）。凭据池按 host 注入会话上下文：LLM 漏写 Cookie 的配方也能以
// 登录态重放，防真实漏洞因重放落到登录页而被机器护栏误证伪。
func (d *Deps) buildEvaluatorAgent(
	ctx context.Context,
	taskID, host, agentRunID, conversationID string,
	cfg agent.Profile,
	skillsView skill.Reader,
	toolDeps func(skill.Reader) tools.Deps,
	reg *registry.ToolRegistry,
	thoughts reasoning.Tap,
) *evaluator.Agent {
	var evaluatorCLImanifest *manifest.ToolManifest
	if d.ToolsManifest != nil && len(cfg.CliTools) > 0 {
		evaluatorCLImanifest = d.ToolsManifest.FilterByNames(cfg.CliTools)
	}
	// 裁决工具走共享拦截器链（遥测/心跳/时间线）——裁决官对目标的动作
	// （复现重放/HTTP 复核）与 executor 同一执行面，不遗漏、归属一致。
	extraTools := tools.BuildTools(toolDeps(skillsView), cfg.FunctionTools)
	for i := range extraTools {
		extraTools[i] = reg.WrapTool(extraTools[i])
	}
	judge := evaluator.NewRouterJudge(d.Router, d.Logger).
		WithFunctionTools(cfg.FunctionTools).
		WithCLIManifest(evaluatorCLImanifest).
		WithSkills(skillCards(ctx, skillsView, d.Logger, "evaluator")).
		WithSystemPrompt(cfg.SystemPrompt).
		WithComplexity(cfg.Complexity).
		WithMaxIterations(cfg.MaxIterations).
		WithMessageWindow(d.Windows.Evaluator).
		WithThoughts(thoughts).
		WithToolWrapper(reg.WrapTool). // judge 自有的 replay_for_verification 同入审计链
		WithExtraTools(extraTools)

	// 复现晋升门：域交付管道（finding.ClaimSink）与指纹归一（finding 形状）
	// 由领域包注入——通用门对领域零感知。坐实发现（晋升 result 的主张，中性
	// 领域词——信息成果与漏洞同为发现）落库即发时间线事件（会话发现卡）。
	claimSink := finding.NewClaimSink(d.Findings, agentRunID)
	if conversationID != "" {
		timeline := d.toolTimelineEvents()
		claimSink = claimSink.WithObserver(func(octx context.Context, f finding.Finding) {
			timeline.Emit(octx, conversationID, map[string]any{
				"kind":       "finding",
				"agent_name": "evaluator",
				"summary":    f.Summary,
				"severity":   f.Severity,
				"host":       f.Host,
				"target":     f.Target,
			})
		})
	}
	promoter := evaluator.New(d.Graph, executor.NewReplayer().WithCredentials(d.Creds, host), claimSink).
		WithJudge(judge). // ReAct 裁决官：replay_for_verification + CLI 复核工具
		WithFingerprintCodec(finding.FingerprintNormalizer{}).
		WithLogger(d.Logger)

	return evaluator.NewAgent(evaluator.AgentConfig{
		TaskID:     taskID,
		AgentRunID: agentRunID,
		Gate:       promoter,
		EventBus:   d.EventBus,
		Graph:      d.Graph,
		Logger:     d.Logger.With().Str("component", "evaluator_agent").Logger(),
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
