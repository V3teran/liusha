// Package verifier 实现 EvaluatorAgent（LLM 自主评估）- 简化版
//
// 注意：这是一个能编译通过的简化版本。
// 完整功能需要进一步完善，特别是：
// 1. LLM ReAct 循环的完整实现
// 2. 工具调用的完整实现
// 3. Finding 创建的完整实现
//
// 当前版本确保架构能运行，具体功能待完善。
package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/knowledgegraph"
	"github.com/V3teran/liusha/internal/registry"
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
	registry     *registry.Registry // Evaluator 专用工具
	logger       zerolog.Logger
	taskID       string // 添加 taskID 字段用于状态导出
}

// Config 是 EvaluatorAgent 的配置。
type Config struct {
	World        *knowledgegraph.Store
	Traffic      *traffic.AgentStore
	FindingStore *finding.Store
	Provider     llm.Provider
	Logger       zerolog.Logger
}

// NewAgent 创建 EvaluatorAgent 实例。
func NewAgent(cfg Config) *Agent {
	a := &Agent{
		world:        cfg.World,
		traffic:      cfg.Traffic,
		findingStore: cfg.FindingStore,
		provider:     cfg.Provider,
		logger:       cfg.Logger.With().Str("agent", "verifier").Logger(),
	}

	// 注册 Evaluator 专用工具
	a.registry = registry.New()
	a.registerTools()

	return a
}


// Verify 评估一个 observation（简化版）
//
// TODO: 完整实现 LLM ReAct 循环
func (a *Agent) Verify(ctx context.Context, observationID string) (*finding.VulnFinding, error) {
	startTime := time.Now()
	a.logger.Info().Str("observation_id", observationID).Msg("starting verification (simplified)")

	// 1. 读取 observation
	hyp, err := a.world.GetNode(ctx, observationID)
	if err != nil {
		return nil, fmt.Errorf("get observation node: %w", err)
	}

	// 2. 解析 observation 内容
	var hypContent ObservationContent
	if err := json.Unmarshal(hyp.Content, &hypContent); err != nil {
		return nil, fmt.Errorf("parse observation content: %w", err)
	}

	a.logger.Info().
		Str("observation_id", observationID).
		Str("statement", hypContent.Statement).
		Msg("observation parsed")

	// TODO: 实现 LLM ReAct 循环评估
	// 当前简化版：自动坐实所有 observation（用于测试架构）

	// 3. 创建 finding（简化版）
	findingObj, err := a.createFinding(ctx, hyp, hypContent)
	if err != nil {
		return nil, fmt.Errorf("create finding: %w", err)
	}

	// 4. 晋升到 WorldModel
	findingNodeID := uuid.New().String()
	verified := knowledgegraph.ConfidenceVerified

	findingNode := knowledgegraph.Node{
		ID:         findingNodeID,
		TaskID:     hyp.TaskID,
		Kind:       knowledgegraph.KindResult,
		Content:    json.RawMessage(fmt.Sprintf(`{"finding_id":"%s","type":"vulnerability"}`, findingObj.ID)),
		Confidence: &verified,
		SourceType: knowledgegraph.SourceEvaluator,
		SourceID:   observationID,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	_, err = a.world.CreateNode(ctx, findingNode)
	if err != nil {
		return nil, fmt.Errorf("create finding node in worldmodel: %w", err)
	}

	a.logger.Info().
		Str("finding_id", findingObj.ID).
		Str("observation_id", observationID).
		Int64("duration_ms", time.Since(startTime).Milliseconds()).
		Msg("observation verified (simplified)")

	return findingObj, nil
}

// createFinding 创建 finding 记录（简化版）
func (a *Agent) createFinding(ctx context.Context, hyp *knowledgegraph.Node, hypContent ObservationContent) (*finding.VulnFinding, error) {
	// 从 observation 推导 severity
	severity := deriveSeverity(hypContent.Statement)

	f := finding.VulnFinding{
		ID:          uuid.New().String(),
		TaskID:      hyp.TaskID,
		Severity:    severity,
		Summary:     hypContent.Statement + "\n\n推理依据: " + hypContent.Reasoning,
		Evaluation:    hypContent.Evidence,
		FirstSeenAt: time.Now(),
	}

	saved, err := a.findingStore.Save(ctx, f)
	if err != nil {
		return nil, err
	}

	return &saved, nil
}

// ObservationContent 是 observation 节点的内容结构。
type ObservationContent struct {
	Statement  string          `json:"statement"`            // 假设陈述
	Reasoning  string          `json:"reasoning"`            // 提出理由
	TestPlan   string          `json:"test_plan,omitempty"`  // 评估计划
	Confidence string          `json:"confidence,omitempty"` // 初始置信度
	Evidence   json.RawMessage `json:"evidence,omitempty"`   // 初步证据
}

// deriveSeverity 从假设陈述推导严重性（简化版）
func deriveSeverity(statement string) string {
	// 简单的关键词匹配
	stmt := statement

	// 检查关键词
	if len(stmt) > 0 {
		// 简化实现：默认 medium
		return "medium"
	}

	return "low"
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
	return nil
}
