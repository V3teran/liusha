package planner

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/logx"
)

// Run 是 planner Agent 的事件循环：订阅 bus 事件 + 轮询图，规划新 action 并写图。
// LLM 配置错误（鉴权/权限/模型不存在）是致命错误：Run 返回该错误，由认知循环
// 终结任务——轮询重撞既不会自愈，失败调用又会被僵尸回收当作「仍有活动」，任务将
// 永远停在 active。
func (a *Agent) Run(ctx context.Context) error {
	// LLM 审计维度：规划调用归 task/本轮 run、角色 planner。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: a.taskID, AgentRunID: a.agentRunID, Role: "planner"})

	a.logger.Info().Msg("Agent 启动")

	// 订阅事件
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 定期检查（兜底机制）
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	// 启动时规划一次
	if err := a.planFailure(a.planActions(ctx), "初始规划"); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Msg("Agent 停止（context done）")
			return ctx.Err()

		case <-a.stopCh:
			a.logger.Info().Msg("Agent 停止")
			return nil

		case event := <-sub.Events():
			// 处理验证结果事件
			if err := a.planFailure(a.handleEvent(ctx, event), "事件触发规划（"+string(event.Type)+"）"); err != nil {
				return err
			}

		case <-ticker.C:
			// 定期兜底：检查是否需要新的规划
			if err := a.planFailure(a.planActions(ctx), "定期规划"); err != nil {
				return err
			}
		}
	}
}

// planFailure 处置一次规划失败：配置错误原样返回（致命，Run 退出）；其余失败记录后
// 返回 nil，等下一次事件或轮询再试（瞬时故障已在 generateNewActions 内退避重试过）。
func (a *Agent) planFailure(err error, phase string) error {
	if err == nil {
		return nil
	}
	if llm.IsConfigError(err) {
		a.logger.Error().Err(err).Str("phase", phase).Msg("LLM 配置错误，规划无法继续")
		return err
	}
	logx.ErrorUnlessCanceled(a.logger, err).Err(err).Str("phase", phase).Msg("规划失败（下一轮重试）")
	return nil
}

// handleEvent 处理事件
func (a *Agent) handleEvent(ctx context.Context, event bus.Event) error {
	switch event.Type {
	case bus.EventVerificationPassed:
		// 验证通过，可能需要新的规划
		a.logger.Info().
			Msg("收到 VerificationPassed 事件，触发新规划")
		return a.planActions(ctx)

	case bus.EventVerificationRefuted:
		// 证伪，需要调整规划
		a.logger.Info().
			Msg("收到 VerificationRefuted 事件，触发调整规划")
		return a.planActions(ctx)

	case bus.EventReplanRequested:
		// P2：Monitor 请求重规划
		reason := ""
		if r, ok := event.Payload["reason"].(string); ok {
			reason = r
		}
		a.logger.Info().
			Str("reason", reason).
			Msg("收到 Monitor 重规划请求，强制重规划")
		return a.forcePlanActions(ctx, reason)

	default:
		// 忽略其他事件
		return nil
	}
}
