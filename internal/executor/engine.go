// Package executor 提供基于 LLM 的 Action 执行引擎
package executor

import (
	"context"
	"encoding/json"
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
	"github.com/V3teran/liusha/internal/tools"
)

// Engine 是基于 LLM + ReAct 的执行引擎
type Engine struct {
	router        *llm.Router
	findings      FindingLister
	registry      *Registry // 使用 executor 包的 Registry
	functionTools []string       // function_tools 白名单（nil=全量）
	toolsManifest ToolsManifest // 过滤后的 CLI 工具清单
	checkpointer  core.Checkpointer
	logger        zerolog.Logger
}

// EngineConfig 配置
type EngineConfig struct {
	Router        *llm.Router
	Findings      FindingLister
	Registry      *Registry
	FunctionTools []string       // function_tools 白名单（agent 配置；nil=全量，空=空集）
	ToolsManifest ToolsManifest // 过滤后的 CLI 工具清单
	Checkpointer  core.Checkpointer
	Logger        zerolog.Logger
}

// NewEngine 创建执行引擎
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{
		router:        cfg.Router,
		findings:      cfg.Findings,
		registry:      cfg.Registry,
		functionTools: cfg.FunctionTools,
		toolsManifest: cfg.ToolsManifest,
		checkpointer:  cfg.Checkpointer,
		logger:        cfg.Logger.With().Str("component", "executor_engine").Logger(),
	}
}

// Execute 执行一个 Action，返回生成的 Attempt 列表
func (e *Engine) Execute(ctx context.Context, action explorationgraph.Node, taskID, host string) ([]evaluator.Attempt, error) {
	// LLM 审计维度：本 Engine 的全部 LLM 调用归 task、角色 executor。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: taskID, Role: "executor"})

	e.logger.Info().
		Str("action_id", action.ID).
		Str("task_id", taskID).
		Msg("开始执行 Action")

	// 1. 解析 Action 内容
	var actionData struct {
		Type        string `json:"type"`
		Instruction string `json:"instruction"`
		Complexity  string `json:"complexity"`
	}
	if err := json.Unmarshal(action.Content, &actionData); err != nil {
		return nil, fmt.Errorf("解析 Action 失败: %w", err)
	}

	// 2. 记录执行前的 finding 快照
	beforeFindings, err := e.findings.ListByTaskAndHost(ctx, taskID, host, 0)
	if err != nil {
		return nil, fmt.Errorf("获取执行前 finding 失败: %w", err)
	}
	seenFindingIDs := make(map[string]bool)
	for _, f := range beforeFindings {
		seenFindingIDs[f.ID] = true
	}

	// 3. 构建 ReAct 执行上下文
	objective := fmt.Sprintf("执行以下操作：%s\n\n类型：%s\n目标：%s",
		actionData.Instruction,
		actionData.Type,
		host)

	// function_tools 白名单先过滤（prompt 与 ReAct 注册共用同一结果，宣传=事实）
	reactTools := filterToolsByName(e.registry.WrappedTools(), e.functionTools)
	systemPrompt := e.buildSystemPrompt(actionData.Type, actionData.Complexity, reactTools)

	// 4. 获取 LLM Provider（Router 已完成 Generator→Provider 桥接与 retry/fallback 装配）
	provider, err := e.router.For(ctx, e.mapComplexityToTier(actionData.Complexity))
	if err != nil {
		return nil, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	// 挂当前 Action ID：write_observation/write_evidence 据此建归属边。
	ctx = tools.WithActionContext(ctx, action.ID)

	// 5. 创建 ReAct Runtime 并注册工具
	reactRuntime := runtime.NewReActRuntime()
	tools := reactTools

	e.logger.Info().
		Int("tool_count", len(tools)).
		Str("action_id", action.ID).
		Msg("🔧 开始注册工具到 ReAct runtime")

	if len(tools) == 0 {
		e.logger.Error().
			Str("action_id", action.ID).
			Msg("❌ 严重错误：没有可用工具！Executor 无法执行任何操作")
		// 仍然继续执行，但会失败
	}

	for _, tool := range tools {
		e.logger.Debug().
			Str("tool_name", tool.Name()).
			Str("action_id", action.ID).
			Msg("注册工具")

		if err := reactRuntime.RegisterTool(tool); err != nil {
			return nil, fmt.Errorf("注册工具 %s 失败: %w", tool.Name(), err)
		}
	}

	// 6. 配置 ReAct 执行
	reactConfig := &runtime.ReActConfig{
		Objective:     objective,
		SystemPrompt:  systemPrompt,
		LLMProvider:   provider,
		MaxIterations: e.getMaxIterations(actionData.Complexity),
		Temperature:   0.7,
		MaxTokens:     4000,
		// 迭代型 agent 统一挂消息压缩链（与 monitor 同构）：长 ReAct 循环的
		// 消息历史无限增长会顶爆上下文窗口，滚动压缩保最近 N 条（executor 窗口 15）。
		MessageModifierChain: runtime.NewDefaultModifierChain(15),

		// Checkpoint 集成（Action 级别不需要）
		Checkpointer:     nil,
		CheckpointPolicy: runtime.NewNeverCheckpointPolicy(),
		TaskID:           taskID,

		OnIteration: func(iteration int, status runtime.IterationStatus) {
			e.logger.Debug().
				Str("action_id", action.ID).
				Int("iteration", iteration).
				Str("status", string(status)).
				Msg("ReAct 迭代")
		},
	}

	// 7. 执行 ReAct 循环
	result, err := reactRuntime.Run(ctx, reactConfig)
	if err != nil {
		return nil, fmt.Errorf("ReAct 执行失败: %w", err)
	}

	e.logger.Info().
		Str("action_id", action.ID).
		Int("iterations", result.Iterations).
		Str("status", string(result.Status)).
		Msg("ReAct 执行完成")

	// 8. 收割新产生的 finding
	afterFindings, err := e.findings.ListByTaskAndHost(ctx, taskID, host, 0)
	if err != nil {
		return nil, fmt.Errorf("获取执行后 finding 失败: %w", err)
	}

	// 9. 转换新 finding 为 Attempt
	var attempts []evaluator.Attempt
	for _, f := range afterFindings {
		if seenFindingIDs[f.ID] {
			continue // 跳过已存在的 finding
		}

		attempt, ok, err := AttemptFromFinding(taskID, f)
		if err != nil {
			e.logger.Error().
				Err(err).
				Str("finding_id", f.ID).
				Msg("转换 finding 为 Attempt 失败")
			continue
		}
		if !ok {
			continue // 跳过无效的 finding
		}

		attempts = append(attempts, attempt)
	}

	e.logger.Info().
		Str("action_id", action.ID).
		Int("new_findings", len(attempts)).
		Msg("收割到新 finding")

	return attempts, nil
}

// filterToolsByName 按 function_tools 白名单过滤（nil=全量，空=空集——严格白名单，
// 与 cli_tools 语义一致）。prompt 工具清单与 ReAct 注册共用同一结果。
func filterToolsByName(tools []registry.Tool, names []string) []registry.Tool {
	if names == nil {
		return tools
	}
	allow := make(map[string]struct{}, len(names))
	for _, n := range names {
		allow[n] = struct{}{}
	}
	out := make([]registry.Tool, 0, len(names))
	for _, t := range tools {
		if _, ok := allow[t.Name()]; ok {
			out = append(out, t)
		}
	}
	return out
}

// buildSystemPrompt 根据 Action 类型构建系统提示词；工具清单段按 registered 渲染
func (e *Engine) buildSystemPrompt(actionType, complexity string, registered []registry.Tool) string {
	var toolSB strings.Builder
	for _, t := range registered {
		toolSB.WriteString("- " + t.Name() + ": " + t.ShortDesc() + "\n")
	}
	if toolSB.Len() == 0 {
		toolSB.WriteString("（无可用工具——function_tools 白名单为空）\n")
	}
	basePrompt := strings.ReplaceAll(`你是一个专业的渗透测试执行专家。你的任务是执行指定的操作，并使用可用的工具完成目标。

**执行原则**：
1. 仔细分析任务目标，制定清晰的执行计划
2. 逐步执行，每次只调用一个工具
3. 根据工具返回结果调整后续步骤
4. 发现可疑漏洞立即用 write_observation 记录假设，用 write_evidence 附上证据
5. 遇到错误时尝试其他方法，不要轻易放弃
6. 完成任务后明确说明"任务完成"

**可用工具**（只能用这些，其余名字不可用）：
{TOOL_LIST}
**漏洞上报流程（差分复现铁律）**：
1. 发现疑似漏洞 → 用 http_request 发良性请求（正常参数）拿 traffic_id 作基线
2. write_observation 附差分配方：repro = {"traffic_id": 良性流量ID, "modifications": {"body_fields": {"id": "1' UNION SELECT 1,2,3--"}}, "assert": {...}}
3. modifications 必填——payload 注入点；原样重放（modifications 空）会被机器直接拒绝
4. assert 断言攻击响应独有特征：报错回显/泄露数据子串（body_contains）、状态改变；时间盲注入用 min_duration_ms（SLEEP(5) 给 4000）
5. 禁止页面常态断言（status_code:200+登录页标题这类正常响应也命中的谓词）——基线同样命中会被拒坐实

`, "{TOOL_LIST}", toolSB.String())

	// ========== 添加过滤后的 CLI 工具清单 ==========
	if e.toolsManifest != nil && len(e.toolsManifest.Tools) > 0 {
		basePrompt += "**沙箱预装工具清单**（通过 run_command 调用）:\n"

		// 按能力轴分组；顺序与 deployments/tool-images/pentools/tools.yaml 的分区一致
		// （PTES 流水线序：侦察 → 发现 → 漏扫 → 注入 → … → 运行时 → 浏览器 → 通用）。
		categoryMap := e.toolsManifest.ByCategory()

		catNames := map[string]string{
			"recon":           "侦察（资产/服务/技术栈发现）",
			"discovery":       "内容/参数发现",
			"vulnscan":        "自动化模板漏扫",
			"injection":       "注入类专项",
			"deserialization": "反序列化 payload 生成",
			"auth":            "认证/凭证攻击",
			"oob":             "带外回调检测",
			"sast":            "源码静态分析",
			"reverse":         "二进制静态逆向",
			"pwn":             "二进制动态利用",
			"cloud":           "云平台攻击",
			"container":       "容器/K8s 攻击",
			"exploitation":    "利用框架/拿初始 shell",
			"post-exploit":    "后渗透（横向/隧道/提权）",
			"crypto":          "密码学攻击",
			"forensics":       "取证",
			"stego":           "隐写术",
			"cracking":        "哈希/密码破解",
			"runtime":         "语言运行时（现场编译/执行）",
			"browser":         "无头浏览器自动化",
			"utility":         "通用胶水（HTTP/JSON/脚本）",
		}

		categoryOrder := []string{
			"recon", "discovery", "vulnscan", "injection", "deserialization", "auth",
			"oob", "sast", "reverse", "pwn", "cloud", "container",
			"exploitation", "post-exploit", "crypto", "forensics", "stego",
			"cracking", "runtime", "browser", "utility",
		}

		// 渲染：先按权威顺序，再兜底渲染清单里新出现、上表未收录的类别（防漏渲染）。
		renderCat := func(cat string) {
			tools, ok := categoryMap[cat]
			if !ok || len(tools) == 0 {
				return
			}
			catName := catNames[cat]
			if catName == "" {
				catName = cat
			}
			basePrompt += fmt.Sprintf("\n**%s**:\n", catName)
			for _, tool := range tools {
				basePrompt += fmt.Sprintf("- %s: %s\n", tool.Name, tool.Description)
			}
		}
		for _, cat := range categoryOrder {
			renderCat(cat)
			delete(categoryMap, cat)
		}
		remaining := make([]string, 0, len(categoryMap))
		for cat := range categoryMap {
			remaining = append(remaining, cat)
		}
		sort.Strings(remaining)
		for _, cat := range remaining {
			renderCat(cat)
		}

		basePrompt += `
**使用示例**：
1. SQL 注入: run_command({"command": "sqlmap -u 'http://target/?id=1' --batch --dbs"})
2. 端口扫描: run_command({"command": "nmap -sV -p80,443,8080 target.com"})
3. 目录爆破: run_command({"command": "feroxbuster -u http://target/ -w /usr/share/wordlists/dirb/common.txt"})

**重要提示**：
- 必须实际调用工具执行测试，不要只做理论分析
- 每次工具调用后仔细分析结果
- 发现可疑漏洞先用 write_observation 记录假设、write_evidence 附上证据，等待 Evaluator 验证坐实

`
	} else {
		e.logger.Warn().Msg("⚠️  没有可用的 CLI 工具（cli_tools 未配置或为空）")
	}
	// ========================================================

	// 根据 Action 类型添加特定指导
	switch actionType {
	case "reconnaissance":
		basePrompt += `
**侦察任务指导**：
- 使用 nmap、masscan 等工具进行端口扫描
- 使用 dirsearch、ffuf 等工具进行目录枚举
- 收集服务版本、技术栈信息
- 记录所有发现的端点和服务
`
	case "vulnerability_scan":
		basePrompt += `
**漏洞扫描指导**：
- 使用 nuclei、sqlmap 等专用扫描工具
- 针对已知服务版本搜索 CVE
- 测试常见漏洞类型（SQL注入、XSS、SSRF等）
- 发现可疑漏洞立即用 write_observation 记录假设并附 write_evidence 证据
`
	case "exploitation":
		basePrompt += `
**漏洞利用指导**：
- 仔细验证漏洞存在性
- 构造精准的 Payload
- 避免破坏性操作
- 成功利用后记录完整的复现步骤
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

	return basePrompt
}

// mapComplexityToTier 将复杂度映射到 LLM 层级
func (e *Engine) mapComplexityToTier(complexity string) llm.Complexity {
	switch complexity {
	case "simple":
		return llm.ComplexitySimple
	case "moderate":
		return llm.ComplexityMedium
	case "complex":
		return llm.ComplexityComplex
	default:
		return llm.ComplexityMedium
	}
}

// getMaxIterations 获取最大迭代次数
func (e *Engine) getMaxIterations(complexity string) int {
	switch complexity {
	case "simple":
		return 5
	case "moderate":
		return 10
	case "complex":
		return 20
	default:
		return 10
	}
}
