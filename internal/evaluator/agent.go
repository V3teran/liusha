// Package evaluator 实现 EvaluatorAgent（LLM 自主评估）
//
// 使用 ReActRuntime 进行 LLM 验证
package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/knowledgegraph"
	"github.com/V3teran/liusha/internal/traffic"
)

// 编译时检查接口实现
var _ core.Agent = (*Agent)(nil)

// Agent 是 EvaluatorAgent，使用 LLM 自主评估 observation。
type Agent struct {
	world        *knowledgegraph.Store
	traffic      *traffic.AgentStore
	findingStore *finding.Store
	provider     llm.Provider
	reactRuntime runtime.ReActRuntime
	logger       zerolog.Logger
	taskID       string // 添加 taskID 字段用于状态导出

	// Checkpoint 系统
	checkpointer     core.Checkpointer
	checkpointPolicy runtime.CheckpointPolicy
}

// Config 是 EvaluatorAgent 的配置。
type Config struct {
	World        *knowledgegraph.Store
	Traffic      *traffic.AgentStore
	FindingStore *finding.Store
	Provider     llm.Provider
	Logger       zerolog.Logger

	// Checkpoint 配置（可选）
	Checkpointer     core.Checkpointer        // nil 表示禁用 checkpoint
	CheckpointPolicy runtime.CheckpointPolicy // nil 使用默认策略
}

// NewAgent 创建 EvaluatorAgent 实例。
func NewAgent(cfg Config) *Agent {
	// 创建 ReAct 运行时
	reactRuntime := runtime.NewReActRuntime()

	// 默认 Checkpoint 策略：每 5 次迭代保存
	checkpointPolicy := cfg.CheckpointPolicy
	if checkpointPolicy == nil && cfg.Checkpointer != nil {
		checkpointPolicy = runtime.NewIterationCheckpointPolicy(5)
	}

	a := &Agent{
		world:            cfg.World,
		traffic:          cfg.Traffic,
		findingStore:     cfg.FindingStore,
		provider:         cfg.Provider,
		reactRuntime:     reactRuntime,
		logger:           cfg.Logger.With().Str("agent", "evaluator").Logger(),
		checkpointer:     cfg.Checkpointer,
		checkpointPolicy: checkpointPolicy,
	}

	// 注册 Evaluator 专用工具
	a.registerTools()

	return a
}

// registerTools 注册评估工具
func (a *Agent) registerTools() {
	tools := []core.Tool{
		NewGetObservationTool(a.world),
		NewGetTrafficTool(a.traffic),
		NewCreateFindingTool(a.findingStore, a.world),
	}

	for _, tool := range tools {
		if err := a.reactRuntime.RegisterTool(tool); err != nil {
			a.logger.Warn().Err(err).Str("tool", tool.Name()).Msg("注册工具失败")
		}
	}
}

// Verify 评估一个 observation（使用 ReActRuntime）
func (a *Agent) Verify(ctx context.Context, observationID string) (*finding.VulnFinding, error) {
	startTime := time.Now()
	a.logger.Info().Str("observation_id", observationID).Msg("starting verification with ReActRuntime")

	// 1. 读取 observation（用于构建 objective）
	hyp, err := a.world.GetNode(ctx, observationID)
	if err != nil {
		return nil, fmt.Errorf("get observation node: %w", err)
	}

	// 2. 解析 observation 内容
	var hypContent ObservationContent
	if err := json.Unmarshal(hyp.Content, &hypContent); err != nil {
		return nil, fmt.Errorf("parse observation content: %w", err)
	}

	// 3. 构建评估目标
	objective := a.buildVerificationObjective(observationID, hypContent)

	// 4. 构建系统提示
	systemPrompt := a.buildSystemPrompt()

	// 5. 配置 ReAct 运行
	config := &runtime.ReActConfig{
		Objective:            objective,
		SystemPrompt:         systemPrompt,
		LLMProvider:          a.provider,
		ModelID:              "deepseek-chat",
		MaxIterations:        20, // 验证可能需要多轮
		Temperature:          0.3, // 较低温度，确保严谨性
		MaxTokens:            8000,
		Tools:                a.reactRuntime.GetTools(),
		MessageModifierChain: runtime.NewEvaluatorModifierChain(),

		// Checkpoint 集成
		Checkpointer:     a.checkpointer,
		CheckpointPolicy: a.checkpointPolicy,
		TaskID:           a.taskID,

		OnIteration: func(iteration int, status runtime.IterationStatus) {
			a.logger.Info().
				Str("observation_id", observationID).
				Int("iteration", iteration).
				Str("status", string(status)).
				Msg("[EVALUATOR] ReAct 迭代")
		},
	}

	// 6. 运行 ReAct 循环
	result, err := a.reactRuntime.Run(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("ReAct runtime execution: %w", err)
	}

	a.logger.Info().
		Str("observation_id", observationID).
		Str("status", string(result.Status)).
		Int("iterations", result.Iterations).
		Int64("duration_ms", time.Since(startTime).Milliseconds()).
		Msg("verification completed")

	// 7. 从工具调用结果中提取 finding
	// 注意：finding 由 create_finding 工具创建并返回
	// 这里我们需要从 result 中提取
	if result.Status == runtime.ReActStatusSuccess {
		// 尝试从最后的工具调用结果中获取 finding_id
		// TODO: 改进结果提取逻辑
		return nil, fmt.Errorf("finding extraction not implemented yet")
	}

	return nil, fmt.Errorf("verification failed: %s", result.Status)
}

// buildVerificationObjective 构建验证目标
func (a *Agent) buildVerificationObjective(observationID string, content ObservationContent) string {
	return fmt.Sprintf(`你是一个漏洞验证 Agent。请验证以下 observation 是否是真实的安全漏洞。

Observation ID: %s
Statement: %s
Reasoning: %s
Evidence: %s

你的任务：
1. 使用 get_observation 工具获取完整的 observation 信息
2. 使用 get_traffic 工具获取相关的流量数据（如果有）
3. 分析证据，判断是否是真实漏洞
4. 如果确认是漏洞，使用 create_finding 工具创建 finding

验证标准：
- 有明确的攻击向量
- 有可复现的 PoC
- 有实际的安全影响
- 证据充分且可信

请开始验证。`, observationID, content.Statement, content.Reasoning, string(content.Evidence))
}

// buildSystemPrompt 构建系统提示
func (a *Agent) buildSystemPrompt() string {
	return `你是一个严谨的安全漏洞验证专家。

你的职责：
1. 评估 observation 的真实性
2. 区分真实漏洞和误报
3. 为确认的漏洞创建 finding

验证原则：
- 证据驱动：基于具体证据而非推测
- 保守判断：不确定时标记为需要人工审核
- 完整分析：考虑攻击面、影响范围、利用难度

Finding 创建规范：
- severity: critical/high/medium/low/info
- summary: 清晰描述漏洞及其影响
- evaluation: 详细的验证过程和证据

请严格按照流程验证。`
}

// ObservationContent 是 observation 节点的内容结构。
type ObservationContent struct {
	Statement  string          `json:"statement"`            // 假设陈述
	Reasoning  string          `json:"reasoning"`            // 提出理由
	TestPlan   string          `json:"test_plan,omitempty"`  // 评估计划
	Confidence string          `json:"confidence,omitempty"` // 初始置信度
	Evidence   json.RawMessage `json:"evidence,omitempty"`   // 初步证据
}

// ============================================
// 实现 framework/core.Agent 接口
// ============================================

// Name 实现 core.Agent 接口
func (a *Agent) Name() string {
	return "evaluator"
}

// Run 实现 core.Agent 接口
// Evaluator 是按需调用的，不是持续运行的 Agent
// Run 方法只需等待 ctx 取消
func (a *Agent) Run(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

// Stop 实现 core.Agent 接口
func (a *Agent) Stop(ctx context.Context) error {
	a.logger.Info().Msg("stopping evaluator agent")
	return nil
}

// ExportState 实现 core.Recoverable 接口
func (a *Agent) ExportState() (json.RawMessage, error) {
	state := map[string]interface{}{
		"task_id": a.taskID,
	}
	return json.Marshal(state)
}

// ImportState 实现 core.Recoverable 接口
func (a *Agent) ImportState(data json.RawMessage) error {
	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if taskID, ok := state["task_id"].(string); ok {
		a.taskID = taskID
	}
	return nil
}
