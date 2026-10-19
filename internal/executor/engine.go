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
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/registry"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// Engine 是基于 LLM + ReAct 的执行引擎
type Engine struct {
	router         *llm.Router
	findings       FindingLister
	graph          *explorationgraph.Store // 探索图存储
	registry       *Registry               // 使用 executor 包的 Registry
	functionTools  []string                // function_tools 白名单（nil=全量）
	toolsManifest  ToolsManifest           // 过滤后的 CLI 工具清单
	skills         []*skill.Card           // Tier 1 skill 索引（agent.skills 声明，正文按需 read_skill)
	charter        string                  // 角色章程（agent.system_prompt，运维经前端可调；空=不渲染）
	complexity     llm.Complexity          // LLM 档位（agent.complexity，文档 complexity 种子 → 三级缓存读；唯一来源）
	maxIt          int                     // ReAct 迭代上限（agent.max_iterations；0=不设限）
	brief          string                  // 任务简报原文（用户指定的入口 URL 等，逐字渲染进 system prompt——防转录漂移）
	agentRunID     string                  // 本轮认知循环的 agent_run.id（LLM 审计归属）
	checkpointer   core.Checkpointer
	traceCollector *TraceCollector // trace 收集器（从 agent_traffic 构建完整执行轨迹）
	logger         zerolog.Logger
}

// EngineConfig 配置
type EngineConfig struct {
	Router         *llm.Router
	Findings       FindingLister
	Graph          *explorationgraph.Store
	Registry       *Registry
	FunctionTools  []string      // function_tools 白名单（agent 配置；nil=全量，空=空集）
	ToolsManifest  ToolsManifest // 过滤后的 CLI 工具清单
	Skills         []*skill.Card // agent.skills 声明的 skill 索引（Tier 1；渐进式加载的目录层）
	SystemPrompt   string        // 角色章程（agent.system_prompt 正文；渲染进 prompt 开头，空=跳过）
	Complexity     string        // LLM 档位（agent.complexity，simple|medium|complex；空回退 medium）
	MaxIterations  int           // ReAct 迭代上限（agent.max_iterations；0=不设限）
	Brief          string        // 任务简报原文（可选；渲染进 system prompt 作入口锚定）
	AgentRunID     string        // 本轮认知循环的 agent_run.id（LLM 审计归属；空 = 审计行不挂 run）
	Checkpointer   core.Checkpointer
	TraceCollector *TraceCollector // trace 收集器（从 agent_traffic 构建完整执行轨迹）
	Logger         zerolog.Logger
}

// NewEngine 创建执行引擎
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{
		router:         cfg.Router,
		findings:       cfg.Findings,
		graph:          cfg.Graph,
		registry:       cfg.Registry,
		functionTools:  cfg.FunctionTools,
		toolsManifest:  cfg.ToolsManifest,
		skills:         cfg.Skills,
		charter:        cfg.SystemPrompt,
		complexity:     llm.ParseComplexity(cfg.Complexity),
		maxIt:          cfg.MaxIterations,
		brief:          cfg.Brief,
		agentRunID:     cfg.AgentRunID,
		checkpointer:   cfg.Checkpointer,
		traceCollector: cfg.TraceCollector,
		logger:         cfg.Logger.With().Str("component", "executor_engine").Logger(),
	}
}

// Execute 执行一个 Action，返回生成的 Attempt 列表
func (e *Engine) Execute(ctx context.Context, action explorationgraph.Node) ([]evaluator.Attempt, error) {
	// 1. 解析 action 内容
	actionData, err := e.parseActionContent(action)
	if err != nil {
		return nil, err
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
		Str("task_id", action.TaskID).
		Msg("开始执行 Action")

	// 4. 准备工具列表
	reactTools := e.prepareReActTools()

	// 5. 构建 ReAct 配置
	reactConfig, err := e.buildReActConfigWithMonitoring(ctx, action, actionData, reactTools)
	if err != nil {
		return nil, err
	}

	// 6. 创建 ReAct Runtime 并注册工具
	reactRuntime := runtime.NewReActRuntime()
	if err := e.registerReActTools(reactRuntime, reactTools, action.ID); err != nil {
		return nil, err
	}

	// 7. 执行 ReAct 循环
	result, err := e.executeReActRuntime(ctx, reactRuntime, reactConfig, action.ID)
	if err != nil {
		return nil, err
	}

	// 8. 检查是否被 monitor kill（早期退出优化）
	if err := e.checkActionAborted(ctx, action.ID); err != nil {
		return nil, err
	}

	// 9. 处理输出并生成 Attempts
	return e.processOutput(ctx, action, result)
}

// buildSystemPrompt 组装 executor system prompt：
//
//	角色章程（agent.system_prompt——DB 事实源，种子 = agents/executor.md 正文，前端可调）
//	+ 动态装配段（工具清单 / CLI 目录 / skill 索引 / actionType 指导 / 时间预期 / 任务简报）
//
// 章程是机制契约（observations 输出契约 / 域信封 repro/assert 规则）的唯一载体：
// 改契约 = 改 agents/executor.md + make reseed，代码不内置第二份静态模板。
// DB 正文为空（新建 agent 未填）时单句兜底，防完全空 prompt 失能。
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
		// 强制运维配置：charter 为空时返回空串，让 ReAct.Run 报错
		// （与 monitor/evaluator 对齐，避免硬编码兜底文案与 agents/executor.md 不同步）
		e.logger.Error().Msg("executor system_prompt 为空，请运行 make reseed")
		return ""
	}

	var promptSB strings.Builder
	promptSB.WriteString(charter)
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
func renderCLICatalog(categoryMap map[string][]manifest.Tool, m *manifest.Manifest) string {
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
