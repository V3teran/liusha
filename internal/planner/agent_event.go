package planner

import (
	"context"
	"errors"
	"fmt"
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

	defer a.releaseCooldown()
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
		a.planFailures, a.planHardFailures = 0, 0
		a.planRetryAt = time.Time{}
		a.releaseCooldown()
		return nil
	}
	// 致命失败 Run 就此退出：撤销冷却的登记，不再为它续约。
	fatal := func() error {
		a.releaseCooldown()
		return err
	}
	if llm.IsConfigError(err) {
		a.logger.Error().Err(err).Str("phase", phase).Msg("模型配置错误，规划无法继续")
		return fatal()
	}
	if errors.Is(err, runtime.ErrMessageTooLong) {
		a.logger.Error().Err(err).Str("phase", phase).Msg("规划输入超过单条消息的上限，重试无济于事，规划无法继续")
		return fatal()
	}
	if errors.Is(err, context.Canceled) {
		// 这一轮被停掉或被目标改写取代：不是规划的失败，循环的下一轮会看到 ctx 已结束
		return nil
	}
	if llm.IsTransient(err) {
		a.planHardFailures = 0
	} else {
		a.planHardFailures++
	}
	logx.ErrorUnlessCanceled(a.logger, err).Err(err).Str("phase", phase).
		Int("consecutive_failures", a.planFailures+1).
		Int("consecutive_hard_failures", a.planHardFailures).
		Dur("cooldown", planBackoff(a.planBackoffBase, a.planFailures+1)).
		Msg("规划失败（冷却后重试）")
	a.planFailures++
	a.planRetryAt = time.Now().Add(planBackoff(a.planBackoffBase, a.planFailures))
	if a.planHardFailures >= persistentPlanFailures {
		a.logger.Error().Err(err).Str("phase", phase).
			Int("consecutive_hard_failures", a.planHardFailures).
			Msg("规划连续非瞬时失败达上限，带着最后一次的原因终止本轮运行")
		a.releaseCooldown()
		return fmt.Errorf("规划连续 %d 次失败: %w", a.planHardFailures, err)
	}
	a.holdCooldown()
	return nil
}

// RunActivity 向所属 run 的存活心跳登记一个在途操作：until 是它最晚结束的时刻，返回的撤销函数
// 在操作结束时调用。接口在使用方定义；*runner.Heartbeats 满足它。
type RunActivity interface {
	Busy(runID string, until time.Time) (release func())
}

// holdCooldown 把冷却期登记为所属 run 在途的有期限等待。恢复监控只凭存活心跳判定一轮 run 是否
// 还在被推进；冷却期里既没有新的模型调用、也没有在途的工具调用，不登记就与停摆无从区分。
// 冷却封顶两分钟，与回收阈值（缺省 120 秒）相当：账户限流时连续几次失败就足以让活着的 run
// 被判停摆、白白重投。到期即失效，下一次失败以新的登记覆盖。
func (a *Agent) holdCooldown() {
	a.releaseCooldown()
	if a.activity != nil {
		a.cooldownRelease = a.activity.Busy(a.agentRunID, a.planRetryAt)
	}
}

// releaseCooldown 撤销冷却期的登记：冷却已结束（成功）、本 run 将退出（致命失败、失败达上限），
// 或要以上一次失败的新登记覆盖它。
func (a *Agent) releaseCooldown() {
	if a.cooldownRelease != nil {
		a.cooldownRelease()
		a.cooldownRelease = nil
	}
}

// persistentPlanFailures 是连续非瞬时失败的上限：到了就带着最后一次的原因终止本轮运行。模型
// 服务的瞬时故障（限流、网络、超时）不计——它可能是额度窗口，等得到；不计上限的确定性失败
// 只会带着冷却一直撞下去，最后被恢复监控按「僵尸、重投」收场，时间线上看不到真正的原因
// （按冷却节奏，10 次约 11 分钟；短暂的图库抖动够不着）。
const persistentPlanFailures = 10

// planBackoffBaseDefault 是冷却时长的起点。
const planBackoffBaseDefault = 5 * time.Second

// planBackoff 是连续失败 n 次之后的冷却时长：从 base 起每次翻倍，封顶 24 个 base（缺省
// 5 秒起、封顶 2 分钟）。base 为零取缺省。
func planBackoff(base time.Duration, failures int) time.Duration {
	if base <= 0 {
		base = planBackoffBaseDefault
	}
	if failures < 1 {
		failures = 1
	}
	if failures > 5 {
		return 24 * base
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
