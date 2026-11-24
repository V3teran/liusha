// loop.go 认知循环的装配与驱动：四 Agent 组装、任务行监视、完成检测。
// （CompletionDetector 在 completion.go；agent 启停 / 任务行监视 / 工具遥测
// 分别见 lifecycle.go / task_watch.go / telemetry.go。）

package cognition

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/bus"
	configstore "github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/convstream"
	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/executor"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/finding"
	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/httprepro"
	"github.com/V3teran/liusha/internal/monitor"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/repro"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/toolinvocation"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
)

// RunActivity 向所属 run 的存活心跳登记一个在途操作：until 是它最晚结束的时刻，返回的撤销函数
// 在操作结束时调用。接口在使用方定义；*runner.Heartbeats 满足它。
type RunActivity interface {
	Busy(runID string, until time.Time) (release func())
}

// Deps 是一次认知循环的全部外部依赖（由 runner 装配注入；全部字段只读）。
// 显式依赖结构让循环装配可以脱离 asynq/handler 单测（白名单/视图/构造链
// 是历史 bug 高发区）。
type Deps struct {
	AssignmentID string // 批次 ID（沙盒工作区一级分组）
	// AgentRunID 是本次认知循环所属的 agent_run.id：模型日志、工具调用、agent 流量与
	// 坐实漏洞都归属到它。由 runner 按投递消息注入——一个任务可有多个 run（暂停后继续、
	// 停机中断续跑、僵尸回收重投），循环不能自行按任务反查（曾取最早的 run，续跑的
	// 产出全部错记到早已结束的首个 run）。
	AgentRunID string
	// Scenario 是任务的场景块（scenario.Render，由 runner 按任务行的场景快照渲染；空 = 无场景）：
	// 四个 agent 的系统提示词末尾拼同一份。与章程并列的独立输入，不拼进章程。
	Scenario string
	Graph    *explorationgraph.Store
	EventBus bus.EventBus
	CfgStore *configstore.Store
	Router   *fwllm.Router
	Logger   zerolog.Logger

	Skills        *skill.StoreReader
	ToolsManifest *manifest.ToolManifest
	// Installed 是沙盒实际安装的工具与浏览器能力（任务开始时查询）。各 agent 的工具名单与它
	// 求交：可用 = 允许 ∩ 已安装，未安装的工具既不注册也不出现在提示词里。
	Installed sandbox.Inventory
	Creds     credential.Provider

	// 遥测后端（toolRecordInterceptor 用）
	ToolCalls *toolinvocation.Store
	Tasks     *task.Store

	// 工具域存储
	Findings *finding.Store
	Corpus   *corpus.Service
	// CorpusScope 是本任务检索知识库的可见范围（任务场景的知识与通用知识）。
	CorpusScope corpus.Scope
	ProxyStore  *traffic.ProxyStore
	AgentStore  *traffic.AgentStore

	MaxActionsPerTask int // 动作预算：一个任务最多执行多少个动作（0 = 不限）

	// 任务宽度与监察节律，取自部署配置（启动期快照，改后重启生效）：ActionParallelism
	// 是一个任务同时执行的动作数上限，VerificationParallelism 是评估者同时验证的假设数
	// 上限，MonitorInterval 是监察者两次全局评估的间隔。它们只表达一个任务想跑多宽；
	// 对模型账户的保护不在这三个数上——同一账户同时在途的请求数由 LLM 层按
	// llm.account_concurrency 统一限制，超出的调用排队等名额。
	ActionParallelism       int
	VerificationParallelism int
	MonitorInterval         time.Duration

	// StepLLMTimeout 是单步 LLM 看门狗上限（系统配置 runtime 组快照），与工具超时上限
	// 一起决定动作停摆的判定时长（executor.StallTimeout）。
	StepLLMTimeout time.Duration

	// RunActivity 把在途的工具调用登记到所属 run 的存活心跳（由 runner 提供；nil = 不登记）。
	// 模型调用由 runner 在调用链上自行登记。
	RunActivity RunActivity

	// ContextRatios 是各角色的上下文历史预算占比（DB compaction 组在任务开始时
	// 的快照，装配层现读传入；预算 = 比例 × 本角色路由 provider 的 context_window，
	// 0 值由压缩器回退内置兜底）。
	ContextRatios ContextRatios

	// ToolLimits 是工具运行时限制（DB runtime 组任务开始时快照；0 值由使用点
	// 回退内置默认）。
	ToolLimits ToolLimits

	// SandboxListener 是沙盒对外可达性契约（config 启动期快照）：监听段 + 回连
	// 地址，注入 run_command 工具描述（详见类型注释）。
	SandboxListener SandboxListener

	// 会话思考流（四 agent 推理实时进时间线）：两者齐备且 conversationID 非空才启用。
	Conversations  *conversation.Store
	EventPublisher *convstream.Publisher
}

// ContextRatios 是各角色上下文历史预算占 config 比例（系统配置 compaction 组快照）。
type ContextRatios struct {
	Planner   float64
	Executor  float64
	Evaluator float64
	Monitor   float64
}

// ToolLimits 是工具运行时限制（系统配置 runtime 组快照）。
type ToolLimits struct {
	StepToolTimeoutSeconds int // run_command 超时钳制上限秒（LLM 自传 timeout_seconds 的上界）
	RunTailBytes           int // run_command stdout/stderr 保留尾部字节数
	// ToolOutputTailBytes 是任意工具输出进 LLM 上下文前的全局尾部保留字节
	// （ReAct 运行时统一收口，个别工具自身的裁剪先行）。
	ToolOutputTailBytes int
}

// SandboxListener 是沙盒对外可达性契约（config sandbox 组启动期快照）：监听段
// 与回连地址。注入 run_command 工具描述——LLM 必须知道「绑哪个端口段、payload
// 里回连地址写什么」，反弹 shell / 代理 / payload 托管才可达。PortCount 0 = 未发布。
type SandboxListener struct {
	PortStart    int
	PortCount    int
	CallbackHost string
}

// Report 是一次 run 的报告：runner 据此收尾，停止原因写入时间线终态节点。
type Report struct {
	StopReason string // 停止原因
	// GoalBasis 是收敛所依据的目标（仅自然收敛时有值）：任务以它为前置落 completed，
	// 收敛之后目标被调整或又有了待执行动作则不结束（见 task.Store.CompleteConverged）。
	GoalBasis explorationgraph.GoalBasis
}

// Run 驱动一次任务的认知循环：四个平级 Agent（planner/executor/evaluator/monitor）
// 经 bus 广播协作，探索图为共享状态面，CompletionDetector 判定收敛后返回报告。
// sb 是沙盒命令执行入口；nil 表示无沙盒（run_command/drive_browser 不注册）。
//
// 任务要做什么在探索图上（根目标，随任务创建、经 task.Store 改写），run 不另带目标。
func Run(ctx context.Context, d *Deps, taskID, host, conversationID string, sb tools.CommandRunner) (Report, error) {
	return d.run(ctx, taskID, host, conversationID, sb)
}

// run 是 Run 的内部实现（拆一层让 Run 的签名稳定、内部演进不惊动调用方）。
func (d *Deps) run(
	ctx context.Context,
	taskID, host, conversationID string,
	sb tools.CommandRunner,
) (Report, error) {
	d.Logger.Info().Msg("[RUN_COGNITION] 启动四Agent架构")

	if d.Graph == nil || taskID == "" || d.EventBus == nil {
		return Report{}, fmt.Errorf("graph and eventBus are required")
	}

	// 根目标代表任务本身，随任务创建（task.Store.Create）；没有根目标就无从规划。
	root, ok, err := d.Graph.RootObjectiveOf(ctx, taskID)
	if err != nil {
		return Report{}, err
	}
	if !ok {
		return Report{}, fmt.Errorf("任务 %s 没有根目标", taskID)
	}

	// 本 run 此刻独占任务：之前的 run 留下的在途动作（进程崩溃、或新旧 run 重叠时
	// 旧 run 尚未交还）交还 open 重新执行。旧 run 若仍活着，它迟到的结果因认领归属
	// 不符而落空。
	reclaimed, err := d.Graph.ReclaimRunningActions(ctx, taskID)
	if err != nil {
		return Report{}, fmt.Errorf("回收上一 run 的在途动作: %w", err)
	}
	if reclaimed > 0 {
		d.Logger.Info().Int("count", reclaimed).Msg("已交还上一 run 留下的在途动作")
	}

	stack, err := d.assembleStack(ctx, taskID, host, conversationID, sb)
	if err != nil {
		return Report{}, err
	}
	// 思考流随循环关闭：冲刷未发增量（终态消息由各 agent 的 Done 落）。
	defer stack.thoughts.Close()

	// 启动四个 Agent（异步；启停封装成 agentLifecycle，主流程收尾与任务行监视共用 Stop）
	lifecycle := newAgentLifecycle(ctx, d.Logger, []agentSpec{
		{name: "planner", start: stack.plannerAgent.Run},
		{name: "executor", start: stack.executorAgent.Run},
		{name: "evaluator", start: stack.evaluatorAgent.Run},
		{name: "monitor", start: stack.monitorAgent.Run},
	}, func(name string, err error) {
		// agent 主动返回 = 致命失败（各 agent 的瞬时故障在内部自愈）：终结任务并上报原因。
		stack.detector.Fail(fmt.Errorf("%s agent: %w", name, err))
	})

	lifecycle.Start()
	defer lifecycle.Stop()

	// 任务行监视：终态 / 暂停 → 停 agents、携精确停止原因结束本 run。
	stopWatch := (&taskWatcher{
		taskID: taskID,
		tasks:  d.Tasks,
		goal:   root.Goal(),
		stopRun: func(reason string) {
			lifecycle.Stop()
			stack.detector.Abort(reason)
		},
		onGoalChanged: func() {
			stack.plannerAgent.RequestReplan("任务目标已被用户调整：按新目标重新规划（按旧目标排出、尚未开始的动作已撤回）", nil)
			stack.monitorAgent.RequestReview("任务目标已被用户调整：复核在途动作是否仍服务于新目标")
		},
		logger: d.Logger.With().Str("component", "task_watch").Logger(),
	}).start(ctx)
	defer stopWatch()

	d.Logger.Info().Msg("四个 Agent 已启动，等待任务完成")

	// 阻塞等待完成检测，随后停止所有 Agent
	result := stack.detector.Start(ctx)
	lifecycle.Stop()

	d.Logger.Info().
		Int64("duration_ms", result.Duration.Milliseconds()).
		Int("actions", result.Actions).
		Int("results", result.Results).
		Str("stop_reason", result.StopReason).
		Msg("认知循环完成")

	report := Report{
		StopReason: result.StopReason,
		GoalBasis:  result.GoalBasis,
	}
	// 致命失败上抛：runner 走 failTask（run 落 error、任务落 aborted、时间线「任务执行失败」带原因）。
	return report, result.Err
}

// agentConfigs 是四个 agent 的生效配置（出厂值叠加用户自定义）。
type agentConfigs struct {
	executor  agent.Agent
	planner   agent.Agent
	evaluator agent.Agent
	monitor   agent.Agent
}

// loadAgentConfigs 经 cfgStore 读四个 agent 的生效配置（用户层走三级缓存），工具名单收窄到
// 沙盒已安装的部分；读失败降级为空配置（章程为空，ReAct 启动即报错）。
// 前端改配置 → 失效广播 → 本进程缓存清 → 下个任务即新值。
func (d *Deps) loadAgentConfigs(ctx context.Context) agentConfigs {
	getCfg := func(code string) agent.Agent {
		cfg, err := d.CfgStore.Agent(ctx, code)
		if err != nil {
			d.Logger.Warn().Err(err).Str("code", code).Msg("加载 agent 配置失败，使用空配置")
			return agent.Agent{Code: code}
		}
		return d.restrictToInstalled(cfg)
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
	taskID, host, conversationID string,
	sb tools.CommandRunner,
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

	agentRunID := d.AgentRunID

	// 工具注册表 + 拦截器链：遥测（tool_invocation 落库 + 节流续命）与时间线
	// （工具过程事件进会话）各一个拦截器，单一职责。跨包工具依赖（executor 注册
	// 与 evaluator BuildTools 共用同一份，避免字面量漂移）。
	activity := executor.NewActivityTracker() // 动作进展：工具调用结束与推理步完成都记一次，看门狗据此判停摆
	reg := registry.New()
	reg.AddInterceptor(d.toolRecordInterceptor(agentRunID, taskID, activity))
	reg.AddInterceptor(d.toolTimelineInterceptor(conversationID))
	toolDeps := func(skillsView skill.Reader) tools.Deps {
		return tools.Deps{
			TaskID:       taskID,
			AssignmentID: d.AssignmentID,
			AgentRunID:   agentRunID,
			Host:         host,
			Logger:       d.Logger,
			Tasks:        d.Tasks,
			Corpus:       d.Corpus,
			CorpusScope:  d.CorpusScope,
			ProxyStore:   d.ProxyStore,
			AgentStore:   d.AgentStore,
			Creds:        d.Creds,
			Sandbox:      sb,         // run_command/drive_browser 依赖；nil 时这两个工具不注册
			Skills:       skillsView, // read_skill 依赖；声明为空时不注册

			// run_command 超时钳制与输出尾部保留（系统配置 runtime 组快照）
			StepToolTimeoutSeconds: d.ToolLimits.StepToolTimeoutSeconds,
			RunTailBytes:           d.ToolLimits.RunTailBytes,

			// 沙盒对外可达性契约（config 启动期快照）：run_command 描述注入
			ListenerPortStart: d.SandboxListener.PortStart,
			ListenerPortCount: d.SandboxListener.PortCount,
			CallbackHost:      d.SandboxListener.CallbackHost,
		}
	}
	tools.RegisterAll(reg, toolDeps(executorSkillView))

	// CLI 工具清单按白名单过滤
	filteredManifest := d.filterManifest(cfgs.executor.CliTools)

	// executor：Engine（ReAct）→ Agent
	executorAgent := d.buildExecutorAgent(ctx, taskID, agentRunID, cfgs.executor,
		filteredManifest, executorSkillView, executor.NewRegistry(reg), thoughts.TapFor("executor"), activity)

	// evaluator：评估者（ReAct judge）→ 复现晋升门 → Agent
	evaluatorAgent := d.buildEvaluatorAgent(ctx, taskID, host, agentRunID, conversationID, cfgs.evaluator,
		evaluatorSkillView, toolDeps, reg, thoughts.TapFor("evaluator"))

	// planner。执行经验经 gatherContext 注入：gatherContext 拉全图观察
	// （含事实类），规划者对执行产物的消费在图上闭环，无需第二套摘要管道。
	// 跨任务知识库只经白名单 function 工具（search_corpus）按需检索，
	// 不在规划输入里预先注入——战略层与执行层同等接通长期记忆。
	// 白名单先与规划安全工具集求交：规划者不做执行面动作（run_command/
	// drive_browser/http_request 是 executor/evaluator 的职责），白名单误配
	// "*" 不得把执行工具武装给规划者——角色边界由代码守护而非运维自觉。
	plannerTools := tools.BuildTools(toolDeps(executorSkillView), intersectPlannerSafeTools(cfgs.planner.FunctionTools))
	for i := range plannerTools {
		plannerTools[i] = reg.WrapTool(plannerTools[i]) // function 工具入共享审计链（遥测/时间线）
	}
	engine := planner.NewEngine(d.Router, d.Logger).
		WithRoundBudget(planningRoundBudget(d.stepLLMTimeout())).
		WithCharter(cfgs.planner.Charter).
		WithScenario(d.Scenario).
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
		WithContextRatio(d.ContextRatios.Planner).
		WithDistiller(newCompactionDistiller(d.Router, taskID, agentRunID, d.Logger)).
		WithCompactionTimeouts(d.compactionTimeouts()).
		WithToolOutputTailBytes(d.ToolLimits.ToolOutputTailBytes).
		WithExtraTools(plannerTools).
		WithThoughts(thoughts.TapFor("planner")).
		WithToolWrapper(reg.WrapTool) // 引擎自有的图读工具入共享审计链（plannerTools 上面已包装）
	// 完成检测器先于规划者构造：规划者宣告收敛时直接通知它。
	detector := NewCompletionDetector(Config{
		TaskID:        taskID,
		Graph:         d.Graph,
		Logger:        d.Logger,
		MaxActions:    d.MaxActionsPerTask, // 0 表示不限
		CheckInterval: 5 * time.Second,
	})

	plannerAgent := planner.NewAgent(planner.AgentConfig{
		TaskID:        taskID,
		AgentRunID:    agentRunID,
		Graph:         d.Graph,
		Planner:       engine,
		EventBus:      d.EventBus,
		Logger:        d.Logger.With().Str("component", "planner_agent").Logger(),
		FunctionTools: cfgs.planner.FunctionTools,
		OnConverged:   detector.Converge,
	})

	// monitor（LLM 档位 = agent.complexity；provider 每次评估现取，见 monitor.evaluate）
	monitorAgent := monitor.New(monitor.Config{
		ContextRatio:        d.ContextRatios.Monitor,
		Distiller:           newCompactionDistiller(d.Router, taskID, agentRunID, d.Logger),
		CompactionTimeouts:  d.compactionTimeouts(),
		ToolOutputTailBytes: d.ToolLimits.ToolOutputTailBytes,
		Thoughts:            thoughts.TapFor("monitor"),
		TaskID:              taskID,
		AgentRunID:          agentRunID,
		FunctionTools:       cfgs.monitor.FunctionTools,
		Charter:             cfgs.monitor.Charter,
		Scenario:            d.Scenario,
		MaxIterations:       cfgs.monitor.MaxIterations,
		Graph:               d.Graph,
		Replanner:           plannerAgent, // 重规划请求直达规划者
		Router:              d.Router,
		Complexity:          cfgs.monitor.Complexity,
		Interval:            d.MonitorInterval,
		WrapTool:            reg.WrapTool, // 监察工具入共享审计链（遥测/时间线）
		Logger:              d.Logger.With().Str("component", "monitor_agent").Logger(),
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

// plannerSafeTools 是规划者可持有的 function 工具上界（只读/检索面）。
// 新增规划安全工具必须在此显式登记——这是角色边界的代码化。
var plannerSafeTools = []string{"evaluate_progress", "observe_state", "search_corpus"}

// intersectPlannerSafeTools 把声明白名单约束到规划安全工具集内（求交，判定同 registry.Allows）：
// 「全部」= 安全集全部，执行面工具无论怎么配都进不来。图读工具（observe_state /
// evaluate_progress）不走 BuildTools、由 planner.Plan 按原白名单自注册，不在求交影响面内。
func intersectPlannerSafeTools(whitelist []string) []string {
	out := make([]string, 0, len(plannerSafeTools))
	for _, n := range plannerSafeTools {
		if registry.Allows(whitelist, n) {
			out = append(out, n)
		}
	}
	return out
}

// filterManifest 按 cli_tools 白名单过滤工具清单（未注入清单或名单为空 → 空集并告警）。
func (d *Deps) filterManifest(cliTools []string) *manifest.ToolManifest {
	if d.ToolsManifest == nil || len(cliTools) == 0 {
		d.Logger.Warn().Msg("未配置 cli_tools，外部工具不可用")
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

// buildExecutorAgent 组装 Engine（ReAct）→ Executor Agent。
func (d *Deps) buildExecutorAgent(
	ctx context.Context,
	taskID, agentRunID string,
	cfg agent.Agent,
	filteredManifest *manifest.ToolManifest,
	skillsView skill.Reader,
	execRegistry *executor.Registry,
	thoughts reasoning.Tap,
	activity *executor.ActivityTracker,
) *executor.Agent {
	engine := executor.NewEngine(executor.EngineConfig{
		Router:              d.Router,
		Graph:               d.Graph,    // observation 落图与持有核对依赖；nil 会在产出观察时空指针崩溃
		AgentRunID:          agentRunID, // 模型日志归属本轮 run
		Registry:            execRegistry,
		FunctionTools:       cfg.FunctionTools,                                 // function_tools 白名单（nil=拒绝，["*"]=全量）
		ToolsManifest:       filteredManifest,                                  // CLI 工具清单（白名单过滤后）
		Skills:              skillCards(ctx, skillsView, d.Logger, "executor"), // Tier 1 skill 索引（正文按需 read_skill）
		Charter:             cfg.Charter,                                       // 角色章程（生效值）
		Scenario:            d.Scenario,                                        // 任务场景块（拼在系统提示词末尾）
		Complexity:          cfg.Complexity,                                    // LLM 档位（agent 生效配置）
		MaxIterations:       cfg.MaxIterations,                                 // ReAct 迭代上限（0=不设限）
		ContextRatio:        d.ContextRatios.Executor,                          // 上下文历史预算占比（系统配置 compaction 组）
		Distiller:           newCompactionDistiller(d.Router, taskID, agentRunID, d.Logger),
		CompactionTimeouts:  d.compactionTimeouts(),
		ToolOutputTailBytes: d.ToolLimits.ToolOutputTailBytes, // 工具输出全局尾部钳制（runtime 收口）
		Thoughts:            thoughts,                         // 思考流（nil=不流式）
		Activity:            activity,                         // 每完成一步推理记一次进展
		Repro:               repro.NewRegistry(httprepro.Kind{}),
		Logger:              d.Logger,
	})

	return executor.NewAgent(executor.AgentConfig{
		TaskID:      taskID,
		AgentRunID:  agentRunID, // 动作认领归属本 run
		Graph:       d.Graph,
		Executor:    engine,
		EventBus:    d.EventBus,
		Logger:      d.Logger.With().Str("component", "executor_agent").Logger(),
		MaxParallel: d.ActionParallelism, // open 动作并发执行（浏览器已按 action 隔离 tab）
		Activity:    activity,
		StallTimeout: executor.StallTimeout(d.stepLLMTimeout(),
			time.Duration(d.ToolLimits.StepToolTimeoutSeconds)*time.Second),
	})
}

// buildEvaluatorAgent 组装评估者（ReAct judge）→ 复现晋升门 → Evaluator Agent。
// http 复现通道零依赖（http 配方自包含完整 HTTP 请求，不从 traffic 表取流量——工具自发
// 流量同样可复现）。凭据池按 host 注入会话上下文：LLM 漏写 Cookie 的配方也能以
// 登录态重放，防真实主张因重放落到登录页而被机器护栏误证伪。
func (d *Deps) buildEvaluatorAgent(
	ctx context.Context,
	taskID, host, agentRunID, conversationID string,
	cfg agent.Agent,
	skillsView skill.Reader,
	toolDeps func(skill.Reader) tools.Deps,
	reg *registry.ToolRegistry,
	thoughts reasoning.Tap,
) *evaluator.Agent {
	var evaluatorCLImanifest *manifest.ToolManifest
	if d.ToolsManifest != nil && len(cfg.CliTools) > 0 {
		evaluatorCLImanifest = d.ToolsManifest.FilterByNames(cfg.CliTools)
	}
	// 判定工具走共享拦截器链（遥测/时间线）——评估者对目标的动作
	// （复现重放/HTTP 复核）与 executor 同一执行面，不遗漏、归属一致。
	extraTools := tools.BuildTools(toolDeps(skillsView), cfg.FunctionTools)
	for i := range extraTools {
		extraTools[i] = reg.WrapTool(extraTools[i])
	}
	judge := evaluator.NewRouterJudge(d.Router, d.Logger).
		WithFunctionTools(cfg.FunctionTools).
		WithCLIManifest(evaluatorCLImanifest).
		WithSkills(skillCards(ctx, skillsView, d.Logger, "evaluator")).
		WithCharter(cfg.Charter).
		WithScenario(d.Scenario).
		WithComplexity(cfg.Complexity).
		WithMaxIterations(cfg.MaxIterations).
		WithContextRatio(d.ContextRatios.Evaluator).
		WithDistiller(newCompactionDistiller(d.Router, taskID, agentRunID, d.Logger)).
		WithCompactionTimeouts(d.compactionTimeouts()).
		WithToolOutputTailBytes(d.ToolLimits.ToolOutputTailBytes).
		WithThoughts(thoughts).
		WithToolWrapper(reg.WrapTool). // judge 自有的 replay_for_verification 同入审计链
		WithExtraTools(extraTools)

	// 复现晋升门：交付管道（finding.ClaimSink，本部署把坐实的主张落进漏洞台账）在此装配，
	// 门本身不认识交付物。坐实的主张落库即发时间线事件（会话发现卡）。
	claimSink := finding.NewClaimSink(d.Findings, agentRunID).
		// 主机兜底：坐实材料抽不出主机时回任务锚定的目标主机（task.target_host）
		WithTaskHostFallback(func(ctx context.Context, taskID string) (string, error) {
			t, err := d.Tasks.GetByID(ctx, taskID)
			if err != nil {
				return "", err
			}
			return t.TargetHost, nil
		})
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
	promoter := evaluator.New(d.Graph, claimSink).
		WithReplayer(httprepro.Name, httprepro.NewReplayer().WithCredentials(d.Creds, host)).
		WithJudge(judge). // ReAct 评估者：replay_for_verification + CLI 复核工具
		WithLogger(d.Logger)

	return evaluator.NewAgent(evaluator.AgentConfig{
		TaskID:     taskID,
		AgentRunID: agentRunID,
		Gate:       promoter,
		EventBus:   d.EventBus,
		Graph:      d.Graph,
		Logger:     d.Logger.With().Str("component", "evaluator_agent").Logger(),
		// 验证并发：每个验证 = 机器复放 + 一轮 judge ReAct（LLM 账单随并发线性放大）
		MaxConcurrent: d.VerificationParallelism,
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

// compactionTimeouts 是各角色压缩链的时间预算，由本 run 的单步 LLM 上限推导：上限热改之后，
// 新开始的 run 按新值算。
func (d *Deps) compactionTimeouts() runtime.CompactionTimeouts {
	return runtime.CompactionTimeoutsFor(d.StepLLMTimeout)
}

// planningRoundBudget 是一轮规划的墙钟上限：单步 LLM 看门狗上限的 2 倍。一轮规划是若干次
// LLM 调用（常见两次：读一遍图面、给出规划），每次调用各自受单步看门狗约束，整轮的预算按
// 「两次调用都跑到上限」定。它不能比单次调用的上限还短——那样调用慢一点整轮就被掐断、
// 下一轮从头再来，被掐断的生成上游照样计费（原先写死 3 分钟、单次上限 5 分钟：实测 10 分钟
// 里 5 轮规划有 2 轮这样作废）。预算不足时规划者按 adaptBudget 放大下一轮。
func planningRoundBudget(stepLLM time.Duration) time.Duration {
	return 2 * stepLLM
}

// stepLLMTimeout 返回单步 LLM 看门狗上限（未配置时取内置默认 300 秒，与配置默认值一致）。
func (d *Deps) stepLLMTimeout() time.Duration {
	if d.StepLLMTimeout > 0 {
		return d.StepLLMTimeout
	}
	return 300 * time.Second
}
