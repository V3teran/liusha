// Package executor 提供基于 LLM 的 Action 执行引擎
package executor

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// Engine 是基于 LLM + ReAct 的执行引擎
type Engine struct {
	router       llm.Router
	findings     FindingLister
	registry     *Registry  // 使用 executor 包的 Registry
	checkpointer core.Checkpointer
	logger       zerolog.Logger
}

// EngineConfig 配置
type EngineConfig struct {
	Router       llm.Router
	Findings     FindingLister
	Registry     *Registry
	Checkpointer core.Checkpointer
	Logger       zerolog.Logger
}

// NewEngine 创建执行引擎
func NewEngine(cfg EngineConfig) *Engine {
	return &Engine{
		router:       cfg.Router,
		findings:     cfg.Findings,
		registry:     cfg.Registry,
		checkpointer: cfg.Checkpointer,
		logger:       cfg.Logger.With().Str("component", "executor_engine").Logger(),
	}
}

// Execute 执行一个 Action，返回生成的 Attempt 列表
func (e *Engine) Execute(ctx context.Context, action explorationgraph.Node, taskID, host string) ([]evaluator.Attempt, error) {
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

	systemPrompt := e.buildSystemPrompt(actionData.Type, actionData.Complexity)

	// 4. 获取 LLM Provider
	provider, err := e.router.For(ctx, e.mapComplexityToTier(actionData.Complexity))
	if err != nil {
		return nil, fmt.Errorf("获取 LLM provider 失败: %w", err)
	}

	// 5. 获取工具列表
	tools := e.registry.Tools()

	// 6. 配置 ReAct 执行
	reactConfig := &runtime.ReActConfig{
		Objective:     objective,
		SystemPrompt:  systemPrompt,
		LLMProvider:   provider,
		ModelID:       e.selectModelID(actionData.Complexity),
		MaxIterations: e.getMaxIterations(actionData.Complexity),
		Temperature:   0.7,
		MaxTokens:     4000,
		Tools:         tools,

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
	reactRuntime := runtime.NewReActRuntime()
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

// buildSystemPrompt 根据 Action 类型构建系统提示词
func (e *Engine) buildSystemPrompt(actionType, complexity string) string {
	basePrompt := `你是一个专业的渗透测试执行专家。你的任务是执行指定的操作，并使用可用的工具完成目标。

**执行原则**：
1. 仔细分析任务目标，制定清晰的执行计划
2. 逐步执行，每次只调用一个工具
3. 根据工具返回结果调整后续步骤
4. 发现漏洞时立即使用 write_finding 工具记录
5. 遇到错误时尝试其他方法，不要轻易放弃
6. 完成任务后明确说明"任务完成"

**可用工具**：
- run_command: 在沙箱中执行命令行工具
- http_request: 发送 HTTP 请求
- write_finding: 记录发现的漏洞
- read_traffic: 读取历史流量记录
- 其他专用工具（根据注册情况）

`

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
- 发现漏洞立即使用 write_finding 记录
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

// selectModelID 选择模型 ID
func (e *Engine) selectModelID(complexity string) string {
	// 这里应该从配置读取，暂时硬编码
	switch complexity {
	case "simple":
		return "deepseek-chat"
	case "moderate":
		return "deepseek-chat"
	case "complex":
		return "deepseek-reasoner"
	default:
		return "deepseek-chat"
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

