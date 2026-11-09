// Package executor 提供基于 LLM 的 Action 执行引擎
package executor

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/reasoning"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// Engine 是基于 LLM + ReAct 的执行引擎
type Engine struct {
	router              *llm.Router
	graph               *explorationgraph.Store // 探索图存储
	registry            *Registry               // 使用 executor 包的 Registry
	functionTools       []string                // function_tools 白名单（nil=拒绝，["*"]=全量）
	toolsManifest       ToolsManifest           // 过滤后的 CLI 工具清单
	skills              []*skill.Card           // Tier 1 skill 索引（agent.skills 声明，正文按需 read_skill)
	charter             string                  // 角色章程（agent.system_prompt，运维经前端可调；空=不渲染）
	complexity          llm.Complexity          // LLM 档位（agent.complexity，文档 complexity 种子 → 三级缓存读；唯一来源）
	maxIt               int                     // ReAct 迭代上限（agent.max_iterations；0=不设限）
	contextRatio        float64                 // 上下文历史预算占 provider.context_window 的比例（0=压缩器内置兜底）
	distiller           runtime.Distiller       // 上下文压缩纪要蒸馏器（nil=压缩降级纯截断）
	toolOutputTailBytes int                     // 工具输出全局尾部钳制字节（0=不钳）
	brief               string                  // 任务简报原文（用户指定的入口 URL 等，逐字渲染进 system prompt——防转录漂移）
	agentRunID          string                  // 本轮认知循环的 agent_run.id（LLM 审计归属）
	traceCollector      *TraceCollector         // trace 收集器（从 agent_traffic 构建完整执行轨迹）
	thoughts            reasoning.Tap           // 思考流出口（nil=不流式）
	logger              zerolog.Logger
}

// EngineConfig 配置
type EngineConfig struct {
	Router        *llm.Router
	Graph         *explorationgraph.Store
	Registry      *Registry
	FunctionTools []string      // function_tools 白名单（agent 配置；nil=拒绝，[]=空集，["*"]=全量）
	ToolsManifest ToolsManifest // 过滤后的 CLI 工具清单
	Skills        []*skill.Card // agent.skills 声明的 skill 索引（Tier 1；渐进式加载的目录层）
	Charter       string        // 角色章程（出厂值叠加用户自定义后的生效值）
	Complexity    string        // LLM 档位（agent.complexity，simple|medium|complex；空回退 medium）
	MaxIterations int           // ReAct 迭代上限（agent.max_iterations；0=不设限）
	// ContextRatio 是上下文历史预算占 provider.context_window 的比例（系统配置
	// compaction 组，任务开始时快照；0=压缩器内置兜底预算）。
	ContextRatio float64
	// Distiller 是上下文压缩纪要蒸馏器（nil=压缩降级纯截断）。
	Distiller runtime.Distiller
	// ToolOutputTailBytes 是工具输出进上下文前的全局尾部钳制字节（0=不钳）。
	ToolOutputTailBytes int
	Brief               string          // 任务简报原文（可选；渲染进 system prompt 作入口锚定）
	AgentRunID          string          // 本轮认知循环的 agent_run.id（LLM 审计归属；空 = 审计行不挂 run）
	TraceCollector      *TraceCollector // trace 收集器（从 agent_traffic 构建完整执行轨迹）
	// Thoughts 是思考流出口（认知循环注入 reasoning.Tap；nil=不流式）。
	Thoughts reasoning.Tap
	Logger   zerolog.Logger
}

// NewEngine 创建执行引擎
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{
		router:              cfg.Router,
		graph:               cfg.Graph,
		registry:            cfg.Registry,
		thoughts:            cfg.Thoughts,
		functionTools:       cfg.FunctionTools,
		toolsManifest:       cfg.ToolsManifest,
		skills:              cfg.Skills,
		charter:             cfg.Charter,
		complexity:          llm.ParseComplexity(cfg.Complexity),
		maxIt:               cfg.MaxIterations,
		contextRatio:        cfg.ContextRatio,
		distiller:           cfg.Distiller,
		toolOutputTailBytes: cfg.ToolOutputTailBytes,
		brief:               cfg.Brief,
		agentRunID:          cfg.AgentRunID,
		traceCollector:      cfg.TraceCollector,
		logger:              cfg.Logger.With().Str("component", "executor_engine").Logger(),
	}
}

// contextBudget 解析历史 token 预算：比例 × 本角色路由 provider 的 context_window。
// 比例未配置/读取失败返回 0——压缩器内置兜底预算接管（配置问题绝不阻断执行）。
func (e *Engine) contextBudget(ctx context.Context) int {
	if e.contextRatio <= 0 {
		return 0
	}
	cw, err := e.router.ContextWindow(ctx, e.complexity)
	if err != nil {
		e.logger.Warn().Err(err).Msg("读取 context_window 失败，压缩预算用内置兜底")
		return 0
	}
	return int(float64(cw) * e.contextRatio)
}

// Execute 执行一个 Action，返回生成的 Attempt 列表
func (e *Engine) Execute(ctx context.Context, action explorationgraph.Node) ([]evaluator.Attempt, Outcome, error) {
	// 1. 解析 action 内容
	actionData, err := e.parseActionContent(action)
	if err != nil {
		return nil, OutcomeDone, err
	}

	// 2. 设置 LLM 审计上下文
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{
		TaskID:     action.TaskID,
		AgentRunID: e.agentRunID,
		Role:       "executor",
	})

	// 3. 设置 action 上下文（供工具使用）
	ctx = tools.WithActionContext(ctx, action.ID)

	e.logger.Info().
		Str("action_id", action.ID).
		Msg("开始执行 Action")

	// 4. 准备工具列表
	reactTools := e.prepareReActTools()

	// 5. kill 传导：action 级 context 在 monitor kill 时取消（在途 LLM/工具即刻退出）
	ctx, kill, stopKillWatch := watchKill(ctx, e.graphReader(), action.ID, killPollInterval, e.logger)
	defer stopKillWatch()

	// 6. 构建 ReAct 配置
	reactConfig, err := e.buildReActConfigWithMonitoring(ctx, action, actionData, reactTools, kill)
	if err != nil {
		return nil, OutcomeDone, err
	}

	// 7. 创建 ReAct Runtime 并注册工具
	reactRuntime := runtime.NewReActRuntime()
	if err := e.registerReActTools(reactRuntime, reactTools, action.ID); err != nil {
		return nil, OutcomeDone, err
	}

	// 8. 执行 ReAct 循环
	result, err := e.executeReActRuntime(ctx, reactRuntime, reactConfig, action.ID)
	if err != nil {
		return nil, OutcomeDone, err
	}

	// 9. 检查是否被 monitor kill（早期退出优化）
	if err := e.checkActionAborted(ctx, action.ID); err != nil {
		return nil, OutcomeDone, err
	}

	// 10. 处理输出并生成 Attempts；迭代预算耗尽（max_iterations）如实上抛为
	// exhausted——强制总结路径仍有结构化产出，attempts 照常发布。
	attempts, perr := e.processOutput(ctx, action, result)
	if perr != nil {
		return nil, OutcomeDone, perr
	}
	outcome := OutcomeDone
	if result.Status == runtime.ReActStatusMaxIterations {
		outcome = OutcomeExhausted
	}
	return attempts, outcome, nil
}

// buildSystemPrompt 组装 executor system prompt：
//
//	章程（可自定义）+ 输出契约（随 ParseOutput 放在本包，不可自定义）
//	+ 动态装配段（工具清单 / CLI 目录 / skill 索引 / actionType 指导 / 时间预期 / 任务简报）
//
// 静态段（章程、契约）在前、动态段在后，利于 provider 侧的前缀缓存。
func (e *Engine) buildSystemPrompt(actionType, complexity string, registered []registry.Tool) string {
	var toolSB strings.Builder
	for _, t := range registered {
		toolSB.WriteString("- " + t.Name() + ": " + t.ShortDesc() + "\n")
	}
	if toolSB.Len() == 0 {
		toolSB.WriteString("（无可用工具——function_tools 白名单为空）\n")
	}

	charter := strings.TrimSpace(e.charter)
	if charter == "" {
		// 自定义层在写入时已拒绝空章程，此处只是兜底：返回空串让 ReAct.Run 报错。
		e.logger.Error().Msg("executor 章程为空")
		return ""
	}

	var promptSB strings.Builder
	promptSB.WriteString(charter)
	promptSB.WriteString("\n\n")
	promptSB.WriteString(contract)
	promptSB.WriteString("\n\n**可用工具**（只能用这些，其余名字不可用）：\n")
	promptSB.WriteString(toolSB.String())
	basePrompt := promptSB.String()

	// ========== 添加过滤后的 CLI 工具清单 ==========
	if e.toolsManifest != nil && len(e.toolsManifest.Tools) > 0 {
		basePrompt += "\n"
		// 按能力轴分组渲染（从 manifest.Categories 读取顺序和名称）
		basePrompt += renderCLICatalog(e.toolsManifest.ByCategory(), e.toolsManifest)
	} else {
		e.logger.Warn().Msg("没有可用的 CLI 工具（cli_tools 未配置或为空）")
	}
	// ========================================================

	// ========== Tier 1 skill 索引（渐进式加载：索引常驻 prompt，正文按需 read_skill）==========
	// 仅在 read_skill 实际注册进 ReAct（function_tools 白名单放行）且本 agent 声明了
	// skills 时渲染——工具不在场还宣传手册入口，LLM 会徒劳调用。
	for _, t := range registered {
		if t.Name() == "read_skill" {
			if idx := skill.RenderIndex(e.skills); idx != "" {
				basePrompt += "\n" + idx
			}
			break
		}
	}
	// ========================================================

	// 根据 Action 类型添加特定指导（领域无关）
	switch actionType {
	case "reconnaissance":
		basePrompt += `
**信息收集任务指导**：
- 使用适当的工具收集目标的基础事实
- 把对后续动作有决策价值的结论记入 observations（category=fact）
`
	case "analysis":
		basePrompt += `
**分析任务指导**：
- 使用专用分析工具
- 针对已知线索搜索相关信息
- 发现可疑现象立即在最终输出的 observations 里记录假设（附 repro 配方）
`
	case "testing":
		basePrompt += `
**测试任务指导**：
- 仔细验证问题存在性
- 构造精准的测试用例
- 避免破坏性操作
- 成功测试后记录完整的复现步骤
`
	}

	// 根据复杂度添加时间建议
	switch complexity {
	case "simple":
		basePrompt += "\n**时间预期**：此任务预计在 5 分钟内完成。\n"
	case "moderate":
		basePrompt += "\n**时间预期**：此任务预计在 15 分钟内完成。\n"
	case "complex":
		basePrompt += "\n**时间预期**：此任务可能需要较长时间，请耐心执行。\n"
	}

	// 任务简报原文（逐字）——用户指定的目标/入口 URL/凭据的唯一权威事实源。
	// 曾因 brief 只在 planner 侧、executor 凭记忆转录入口 URL（/login.php→/login）
	// 打在 404 上浪费整轮；逐字附录比任何摘要/解析都可靠。
	if e.brief != "" {
		basePrompt += "\n**任务简报（用户原文，目标/入口 URL/凭据以此为准）**：\n" + e.brief + "\n"
	}

	return basePrompt
}

// renderCLICatalog 把过滤后的 CLI 工具清单按类别渲染进 prompt。
// 使用 manifest.Categories 的顺序和名称，避免硬编码。
func renderCLICatalog(categoryMap map[string][]manifest.Tool, m *manifest.ToolManifest) string {
	var b strings.Builder
	b.WriteString("**沙箱预装工具清单**（通过 run_command 调用）:\n")

	catByCode := m.CategoryByCode()
	seen := make(map[string]bool, len(categoryMap))

	// 按 manifest 定义的顺序渲染
	for _, cat := range m.OrderedCategories() {
		tools, ok := categoryMap[cat.Code]
		if !ok || len(tools) == 0 {
			continue
		}
		seen[cat.Code] = true
		fmt.Fprintf(&b, "\n**%s**:\n", cat.Name)
		for _, tool := range tools {
			fmt.Fprintf(&b, "- %s: %s\n", tool.Name, tool.Description)
		}
	}

	// 兜底：渲染未在 categories 中定义的类别
	remaining := make([]string, 0, len(categoryMap))
	for cat := range categoryMap {
		if !seen[cat] {
			remaining = append(remaining, cat)
		}
	}
	sort.Strings(remaining)
	for _, cat := range remaining {
		tools := categoryMap[cat]
		if len(tools) == 0 {
			continue
		}
		name := cat
		if c, ok := catByCode[cat]; ok {
			name = c.Name
		}
		fmt.Fprintf(&b, "\n**%s**:\n", name)
		for _, tool := range tools {
			fmt.Fprintf(&b, "- %s: %s\n", tool.Name, tool.Description)
		}
	}

	b.WriteString("\n**重要提示**：必须实际调用工具执行测试，不要只做理论分析；每次调用后仔细分析结果再决定下一步。\n")
	return b.String()
}
