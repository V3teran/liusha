package planner

import (
	"context"
	"errors"
	"time"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
	"github.com/V3teran/liusha/internal/logx"
)

// Run 是 planner Agent 的事件循环：订阅 bus 事件 + 轮询图，规划新 action 并写图。
// 模型配置错误（鉴权/权限/模型不存在）是致命错误：Run 返回该错误，由认知循环
// 终结任务——轮询重撞既不会自愈，失败调用又会被僵尸回收当作「仍有活动」，任务将
// 永远停在 active。
func (a *Agent) Run(ctx context.Context) error {
	// 模型调用维度：规划调用归 task/本轮 run、角色 planner。
	ctx = llm.WithCallMeta(ctx, llm.CallMeta{TaskID: a.taskID, AgentRunID: a.agentRunID, Role: "planner"})

	a.logger.Info().Msg("Agent 启动")

	// 订阅事件
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 定期检查（兜底机制）
	ticker := time.NewTicker(a.pollInterval)
	defer ticker.Stop()

	plan := func(c context.Context) error { return a.planningRound(c, a.planActions) }

	// 启动时规划一次
	if err := a.planFailure(plan(ctx), "初始规划"); err != nil {
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

		case <-a.replanCh:
			reasons := a.liveReplanReasons(ctx, a.takeReplanRequests())
			if len(reasons) == 0 {
				continue
			}
			a.logger.Info().Strs("reasons", reasons).Msg("收到重规划请求，强制重规划")
			if err := a.planFailure(a.planningRound(ctx, func(c context.Context) error {
				return a.forcePlanActions(c, reasons)
			}), "强制重规划"); err != nil {
				return err
			}

		case event := <-sub.Events():
			// 冷却期里事件触发的规划一并跳过：冷却一过，兜底轮询会接上
			if a.coolingDown() {
				continue
			}
			// 处理验证结果事件
			if err := a.planFailure(a.handleEvent(ctx, event, plan), "事件触发规划（"+string(event.Type)+"）"); err != nil {
				return err
			}

		case <-ticker.C:
			// 定期兜底：检查是否需要新的规划
			if a.coolingDown() {
				continue
			}
			if err := a.planFailure(plan(ctx), "定期规划"); err != nil {
				return err
			}
		}
	}
}

// planFailure 处置一次规划失败。
//
// 输入超过单条消息的上限与模型配置错误一样致命，原样返回（Run 退出）：拼装这条输入的图面
// 没有变，重试多少次都一样。曾有一轮规划因此在两个半小时里失败了 1342 次——事件与轮询
// 每隔几秒就再撞一次，任务空转到最后被恢复监控按重投上限终止。
//
// 其余失败记录后进入冷却，等下一次事件或轮询再试（瞬时故障已在 generateNewActions 内退避
// 重试过；被目标改写取代的那一轮在 planningRound 里已经不算失败）。冷却随连续失败的次数
// 指数增长（见 planBackoff），成功一次即清零。成功时也经由这里清零。
func (a *Agent) planFailure(err error, phase string) error {
	if err == nil {
		a.planFailures = 0
		a.planRetryAt = time.Time{}
		return nil
	}
	if llm.IsConfigError(err) {
		a.logger.Error().Err(err).Str("phase", phase).Msg("模型配置错误，规划无法继续")
		return err
	}
	if errors.Is(err, runtime.ErrMessageTooLong) {
		a.logger.Error().Err(err).Str("phase", phase).Msg("规划输入超过单条消息的上限，重试无济于事，规划无法继续")
		return err
	}
	logx.ErrorUnlessCanceled(a.logger, err).Err(err).Str("phase", phase).
		Int("consecutive_failures", a.planFailures+1).
		Dur("cooldown", planBackoff(a.planFailures+1)).
		Msg("规划失败（冷却后重试）")
	a.planFailures++
	a.planRetryAt = time.Now().Add(planBackoff(a.planFailures))
	return nil
}

// planBackoff 是连续失败 n 次之后的冷却时长：5 秒起每次翻倍，封顶 2 分钟。
func planBackoff(failures int) time.Duration {
	const base, ceiling = 5 * time.Second, 2 * time.Minute
	if failures < 1 {
		failures = 1
	}
	if failures > 5 {
		return ceiling
	}
	return base << (failures - 1)
}

// coolingDown 报告是否还在上一次规划失败之后的冷却期里。
func (a *Agent) coolingDown() bool {
	return time.Now().Before(a.planRetryAt)
}

// handleEvent 处理事件
// handleEvent 处理事件；plan 是跑一轮常规规划的入口（带在途取消登记）。
func (a *Agent) handleEvent(ctx context.Context, event bus.Event, plan func(context.Context) error) error {
	switch event.Type {
	case bus.EventVerificationPassed:
		// 验证通过，可能需要新的规划
		a.logger.Info().
			Msg("收到 VerificationPassed 事件，触发新规划")
		return plan(ctx)

	case bus.EventVerificationRefuted:
		// 证伪，需要调整规划
		a.logger.Info().
			Msg("收到 VerificationRefuted 事件，触发调整规划")
		return plan(ctx)

	default:
		// 忽略其他事件
		return nil
	}
}
