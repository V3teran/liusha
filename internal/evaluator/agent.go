// Package evaluator 提供验证层（Agent），负责验证 Observation 并决定是否晋升为 Evaluation/Result 节点
package evaluator

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/framework/core"
)

// 编译时检查接口实现
var _ core.Agent = (*Agent)(nil)

// Agent 是异步验证 Agent
//
// 职责：
// - 监听 EventAttemptGenerated 事件（executor 产出的漏洞候选 Attempt）
// - 经 PromotionEvaluator 复现门验证后晋升（对应铁律：图里只存坐实态）
// - 发布 EventVerificationPassed/Refuted 事件
type Agent struct {
	taskID        string
	evaluator     *PromotionEvaluator // 使用具体类型
	eventBus      bus.Bus
	logger        zerolog.Logger
	maxConcurrent int
	stopCh        chan struct{}
}

// AgentConfig 配置
type AgentConfig struct {
	TaskID        string
	Evaluator     *PromotionEvaluator
	EventBus      bus.Bus
	Logger        zerolog.Logger
	MaxConcurrent int
}

// NewAgent NewEvaluatorAgent 创建 Agent。
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.MaxConcurrent <= 0 {
		cfg.MaxConcurrent = 1
	}

	return &Agent{
		taskID:        cfg.TaskID,
		evaluator:     cfg.Evaluator,
		eventBus:      cfg.EventBus,
		logger:        cfg.Logger.With().Str("agent", "evaluator").Logger(),
		maxConcurrent: cfg.MaxConcurrent,
		stopCh:        make(chan struct{}),
	}
}

// Run 实现 core.Agent 接口
func (a *Agent) Run(ctx context.Context) error {
	if a.evaluator == nil {
		return fmt.Errorf("evaluator: PromotionEvaluator is required")
	}

	a.logger.Info().Str("task_id", a.taskID).Msg("Agent 启动")

	// 订阅事件
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 并发控制（信号量）
	sem := make(chan struct{}, a.maxConcurrent)

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止（context done）")
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Str("task_id", a.taskID).Msg("Agent 停止")
			return nil

		case event := <-sub.Events():
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
					}
				}(actionID, attempt)
			}
		}
	}
}

// verifyAttempt 验证单个 Attempt
func (a *Agent) verifyAttempt(ctx context.Context, actionID string, attempt Attempt) error {
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
func (a *Agent) Name() string {
	return "evaluator"
}

// Stop 实现 core.Agent 接口
func (a *Agent) Stop(_ context.Context) error {
	a.logger.Info().Msg("停止 evaluator agent")
	close(a.stopCh)
	return nil
}
