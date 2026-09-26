// Package evaluator 提供验证层（EvaluatorAgent），负责验证 Observation 并决定是否晋升为 Evaluation/Result 节点
package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// 编译时检查接口实现
var _ core.Agent = (*EvaluatorAgent)(nil)

// EvaluatorAgent 是异步验证 Agent
//
// 职责：
// - 监听 EventObservationCreated 事件（新）
// - 监听 EventAttemptGenerated 事件（保留兼容）
// - 验证 Observation/Attempt 并决定是否晋升
// - 发布 EventVerificationPassed/Failed/Refuted 事件
type EvaluatorAgent struct {
	taskID        string
	evaluator     *PromotionEvaluator // 使用具体类型
	world         nodeReader          // 添加：用于读取 Observation
	eventBus      bus.Bus
	logger        zerolog.Logger
	maxConcurrent int
	stopCh        chan struct{}
}

// EvaluatorAgentConfig 配置
type EvaluatorAgentConfig struct {
	TaskID        string
	Evaluator     *PromotionEvaluator
	World         nodeReader // 添加：用于读取 Observation
	EventBus      bus.Bus
	Logger        zerolog.Logger
	MaxConcurrent int
}

// nodeReader 定义读取节点的接口
type nodeReader interface {
	GetNode(ctx context.Context, nodeID string) (*explorationgraph.Node, error)
	CreateEdge(ctx context.Context, edge *core.GraphEdge) error
}

// NewEvaluatorAgent 创建 EvaluatorAgent
func NewEvaluatorAgent(cfg EvaluatorAgentConfig) *EvaluatorAgent {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}

	return &EvaluatorAgent{
		taskID:        cfg.TaskID,
		evaluator:     cfg.Evaluator,
		world:         cfg.World,
		eventBus:      cfg.EventBus,
		logger:        cfg.Logger.With().Str("agent", "evaluator").Logger(),
		maxConcurrent: cfg.MaxConcurrent,
		stopCh:        make(chan struct{}),
	}
}

// Start 启动 EvaluatorAgent（异步运行）
func (a *EvaluatorAgent) Start(ctx context.Context) error {
	a.logger.Info().Str("task_id", a.taskID).Msg("EvaluatorAgent 启动")

	// 订阅事件
	eventCh := a.eventBus.SubscribeTask(a.taskID)
	defer a.eventBus.UnsubscribeTask(a.taskID)

	// 并发控制（信号量）
	sem := make(chan struct{}, a.maxConcurrent)

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("EvaluatorAgent 停止（context done）")
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Str("task_id", a.taskID).Msg("EvaluatorAgent 停止")
			return nil

		case event := <-eventCh:
			// 处理 ObservationCreated 事件（新流程）
			if event.Type == bus.EventObservationCreated {
				observationID, ok := event.Payload["observation_id"].(string)
				if !ok {
					a.logger.Warn().Interface("payload", event.Payload).Msg("ObservationCreated 缺少 observation_id")
					continue
				}

				// 异步处理（并发控制）
				sem <- struct{}{} // 获取信号量
				go func(obsID string) {
					defer func() { <-sem }() // 释放信号量

					a.logger.Info().
						Str("observation_id", obsID).
						Msg("开始评估 Observation")

					if err := a.evaluateObservation(ctx, obsID); err != nil {
						a.logger.Error().
							Err(err).
							Str("observation_id", obsID).
							Msg("评估 Observation 失败")
					}
				}(observationID)
				continue
			}

			// 处理 AttemptGenerated 事件（保留兼容旧流程）
			if event.Type == bus.EventAttemptGenerated {
				actionID, ok := event.Payload["action_id"].(string)
				if !ok {
					a.logger.Warn().Interface("payload", event.Payload).Msg("AttemptGenerated 缺少 action_id")
					continue
				}

				attemptPayload, ok := event.Payload["attempt"]
				if !ok {
					a.logger.Warn().Msg("AttemptGenerated 缺少 attempt")
					continue
				}

				// 类型断言为 Attempt
				attempt, ok := attemptPayload.(Attempt)
				if !ok {
					a.logger.Warn().Msg("attempt 类型错误")
					continue
				}

				// 异步验证（并发控制）
				sem <- struct{}{} // 获取信号量
				go func(actionID string, attempt Attempt) {
					defer func() { <-sem }() // 释放信号量

					a.logger.Info().
						Str("action_id", actionID).
						Msg("开始验证 Attempt")

					if err := a.verifyAttempt(ctx, actionID, attempt); err != nil {
						a.logger.Error().
							Err(err).
							Str("action_id", actionID).
							Msg("验证失败")

						// 发布验证失败事件
						a.eventBus.PublishVerificationFailed(a.taskID, actionID, err)
					}
				}(actionID, attempt)
			}
		}
	}
}

// verifyAttempt 验证单个 Attempt
func (a *EvaluatorAgent) verifyAttempt(ctx context.Context, actionID string, attempt Attempt) error {
	startTime := time.Now()

	// 调用 PromotionEvaluator 验证
	node, err := a.evaluator.Promote(ctx, attempt)
	if err != nil {
		return fmt.Errorf("promote: %w", err)
	}

	// node == nil 表示验证证伪
	if node == nil {
		a.logger.Info().
			Str("action_id", actionID).
			Int64("duration_ms", time.Since(startTime).Milliseconds()).
			Msg("验证证伪（未晋升）")

		a.eventBus.PublishVerificationRefuted(a.taskID, actionID)
		return nil
	}

	// 验证通过，已晋升
	a.logger.Info().
		Str("action_id", actionID).
		Str("node_id", node.ID).
		Str("node_kind", string(node.Kind)).
		Int64("duration_ms", time.Since(startTime).Milliseconds()).
		Msg("验证通过，已晋升")

	// 发布验证通过事件
	a.eventBus.PublishVerificationPassed(a.taskID, node.ID)

	return nil
}

// evaluateObservation 评估单个 Observation 节点
func (a *EvaluatorAgent) evaluateObservation(ctx context.Context, observationID string) error {
	startTime := time.Now()

	// 1. 从数据库读取 Observation 节点
	observation, err := a.world.GetNode(ctx, observationID)
	if err != nil {
		return fmt.Errorf("get observation node: %w", err)
	}

	if observation.Kind != core.KindObservation {
		return fmt.Errorf("node %s is not an observation (kind=%s)", observationID, observation.Kind)
	}

	// 2. 解析 Observation 内容
	var obsContent struct {
		Success       bool     `json:"success"`
		Status        string   `json:"status"`
		ActionID      string   `json:"action_id"`
		Findings      []string `json:"findings,omitempty"`
		AttemptsCount int      `json:"attempts_count"`
	}
	if err := json.Unmarshal(observation.Content, &obsContent); err != nil {
		return fmt.Errorf("unmarshal observation content: %w", err)
	}

	// 3. 判断是否值得创建 Result
	// 简化策略：执行成功的 Observation 可以创建 Result（作为执行证据）
	// 如果有 attempts（漏洞），走旧的验证流程；否则创建简单的执行记录 Result
	if !obsContent.Success {
		a.logger.Debug().
			Str("observation_id", observationID).
			Bool("success", obsContent.Success).
			Msg("Observation 执行失败，不创建 Result")
		return nil
	}

	// 4. 创建 Result 节点
	if err := a.createResultFromObservation(ctx, observation, obsContent); err != nil {
		return fmt.Errorf("create result: %w", err)
	}

	a.logger.Info().
		Str("observation_id", observationID).
		Str("action_id", obsContent.ActionID).
		Int64("duration_ms", time.Since(startTime).Milliseconds()).
		Msg("从 Observation 创建 Result 成功")

	return nil
}

// createResultFromObservation 从 Observation 创建 Result 节点
func (a *EvaluatorAgent) createResultFromObservation(
	ctx context.Context,
	observation *explorationgraph.Node,
	obsContent struct {
		Success       bool     `json:"success"`
		Status        string   `json:"status"`
		ActionID      string   `json:"action_id"`
		Findings      []string `json:"findings,omitempty"`
		AttemptsCount int      `json:"attempts_count"`
	},
) error {
	// 构建 Result 内容
	resultContent := map[string]interface{}{
		"type":           "execution_record",
		"observation_id": observation.ID,
		"action_id":      obsContent.ActionID,
		"status":         obsContent.Status,
		"summary":        fmt.Sprintf("Action %s 执行成功", obsContent.ActionID[:8]),
	}

	// 如果有 findings 摘要，记录
	if len(obsContent.Findings) > 0 {
		resultContent["findings_summary"] = obsContent.Findings
		resultContent["type"] = "vulnerability_candidate"
	}

	// 评估过程记录
	evaluation := fmt.Sprintf(
		"基于 Observation %s 的评估。执行状态：%s，attempts: %d。",
		observation.ID[:8],
		obsContent.Status,
		obsContent.AttemptsCount,
	)

	resultContentJSON, err := json.Marshal(resultContent)
	if err != nil {
		return fmt.Errorf("marshal result content: %w", err)
	}

	// 创建 Result 节点
	verified := explorationgraph.ConfidenceVerified
	resultID := uuid.New().String()

	result := explorationgraph.Node{
		ID:         resultID,
		TaskID:     observation.TaskID,
		Kind:       core.KindResult,
		Content:    resultContentJSON,
		Confidence: &verified,
		Priority:   observation.Priority,
		SourceType: explorationgraph.SourceType("verifier"), // 使用数据库约束允许的值
		SourceID:   observation.ID,
		Metadata: json.RawMessage(fmt.Sprintf(`{
			"observation_id": "%s",
			"evaluation": "%s"
		}`, observation.ID, evaluation)),
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	_, err = a.evaluator.world.CreateNode(ctx, result)
	if err != nil {
		return fmt.Errorf("create result node: %w", err)
	}

	// 创建边：Observation → Result
	err = a.world.CreateEdge(ctx, &core.GraphEdge{
		From:      observation.ID,
		To:        resultID,
		Relation:  string(core.RelationConfirms), // Observation 确认为 Result
		CreatedAt: time.Now(),
	})
	if err != nil {
		a.logger.Error().Err(err).Msg("创建 Observation → Result 边失败")
		// 不返回错误，节点已创建
	}

	a.logger.Info().
		Str("result_id", resultID).
		Str("observation_id", observation.ID).
		Msg("Result 节点已创建")

	// 发布 ResultCreated 事件
	a.eventBus.PublishResultCreated(observation.TaskID, resultID)

	return nil
}

// ============================================
// 实现 framework/core.Agent 接口
// ============================================

// Name 实现 core.Agent 接口
func (a *EvaluatorAgent) Name() string {
	return "evaluator"
}

// Run 实现 core.Agent 接口（调用 Start）
func (a *EvaluatorAgent) Run(ctx context.Context) error {
	return a.Start(ctx)
}

// Stop 实现 core.Agent 接口
func (a *EvaluatorAgent) Stop(ctx context.Context) error {
	a.logger.Info().Msg("停止 evaluator agent")
	close(a.stopCh)
	return nil
}

// ExportState 实现 core.Recoverable 接口
func (a *EvaluatorAgent) ExportState() (json.RawMessage, error) {
	state := map[string]interface{}{
		"task_id": a.taskID,
	}
	return json.Marshal(state)
}

// ImportState 实现 core.Recoverable 接口
func (a *EvaluatorAgent) ImportState(data json.RawMessage) error {
	var state map[string]interface{}
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if taskID, ok := state["task_id"].(string); ok {
		a.taskID = taskID
	}
	return nil
}
