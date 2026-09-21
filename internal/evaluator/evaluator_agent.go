// Package evaluator 提供验证层（EvaluatorAgent），负责验证 Observation 并决定是否晋升为 Evaluation/Result 节点
package evaluator

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/framework/core"
)

// 编译时检查接口实现
var _ core.Agent = (*EvaluatorAgent)(nil)

// EvaluatorAgent 是异步验证 Agent
//
// 职责：
// - 监听 EventAttemptGenerated 事件
// - 验证 Attempt 并决定是否晋升
// - 发布 EventVerificationPassed/Failed/Refuted 事件
type EvaluatorAgent struct {
	taskID        string
	evaluator     *PromotionEvaluator // 使用具体类型
	eventBus      bus.Bus
	logger        zerolog.Logger
	maxConcurrent int
	stopCh        chan struct{}
}

// EvaluatorAgentConfig 配置
type EvaluatorAgentConfig struct {
	TaskID        string
	Evaluator     *PromotionEvaluator
	EventBus      bus.Bus
	Logger        zerolog.Logger
	MaxConcurrent int
}

// NewEvaluatorAgent 创建 EvaluatorAgent
func NewEvaluatorAgent(cfg EvaluatorAgentConfig) *EvaluatorAgent {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}

	return &EvaluatorAgent{
		taskID:        cfg.TaskID,
		evaluator:     cfg.Evaluator,
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
			// 只处理 AttemptGenerated 事件
			if event.Type != bus.EventAttemptGenerated {
				continue
			}

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
