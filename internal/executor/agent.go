// Package executor 提供执行层的 Agent
//
// Agent 是持续运行的异步执行器，通过事件驱动响应 Action，生成 Observation
package executor

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/logx"
)

// Agent 是执行者：按优先级认领 open 动作并执行，观察落图后把新提出的假设交给评估者。
type Agent struct {
	graph       *explorationgraph.Store
	executor    ActionExecutor
	eventBus    bus.EventBus
	logger      zerolog.Logger
	taskID      string
	agentRunID  string // 本 run 的 ID：认领动作时记下，结束与交还以此校验归属
	maxParallel int    // 并发执行的 action 上限（1=串行）

	// 看门狗：在途动作超过 stallTimeout 无进展即取消（见 watchdog.go）
	activity           *ActivityTracker
	stallTimeout       time.Duration
	stallCheckInterval time.Duration
	inFlightMu         sync.Mutex
	inFlight           map[string]inFlightAction

	// 执行池：slots 是已派发（含认领中）的动作，workers 等在途执行收尾，
	// slotFreed 唤醒补位，failures 把执行失败交回主循环判断是否致命。
	slotsMu   sync.Mutex
	slots     map[string]struct{}
	workers   sync.WaitGroup
	slotFreed chan struct{}
	failures  chan error
}

// AgentConfig 配置 Agent
type AgentConfig struct {
	TaskID      string
	AgentRunID  string // 本 run 的 ID（动作认领归属）
	Graph       *explorationgraph.Store
	Executor    ActionExecutor
	EventBus    bus.EventBus
	Logger      zerolog.Logger
	MaxParallel int // 并发执行 action 上限（0/1=串行）

	// Activity 是动作进展记录（工具拦截器与执行引擎写入，看门狗读取）；nil 时自建。
	Activity *ActivityTracker
	// StallTimeout 是判定停摆的无进展时长（见 StallTimeout）；0 = 不启用看门狗。
	StallTimeout time.Duration
	// StallCheckInterval 是看门狗巡检周期（0 = 30 秒）。
	StallCheckInterval time.Duration
}

// NewAgent 创建 Agent
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.Activity == nil {
		cfg.Activity = NewActivityTracker()
	}
	if cfg.StallCheckInterval <= 0 {
		cfg.StallCheckInterval = 30 * time.Second
	}
	if cfg.MaxParallel <= 0 {
		cfg.MaxParallel = 1 // 默认串行（保守）；>1 时 open 动作并发执行
	}

	return &Agent{
		graph:       cfg.Graph,
		executor:    cfg.Executor,
		eventBus:    cfg.EventBus,
		logger:      cfg.Logger.With().Str("agent", "executor").Logger(),
		taskID:      cfg.TaskID,
		agentRunID:  cfg.AgentRunID,
		maxParallel: cfg.MaxParallel,

		activity:           cfg.Activity,
		stallTimeout:       cfg.StallTimeout,
		stallCheckInterval: cfg.StallCheckInterval,
		inFlight:           map[string]inFlightAction{},

		slots:     map[string]struct{}{},
		slotFreed: make(chan struct{}, 1),
		failures:  make(chan error, cfg.MaxParallel),
	}
}

// Run 启动执行循环（ctx 取消即停止）：有空位就按优先级认领下一个 open 动作执行。
// 空位释放、规划者提出新动作、定时兜底三种时机都会补位；没有整批屏障——一个慢动作
// 不会挡住后面的动作。返回前等在途执行收尾（交还或结束落图）。何时结束整个 run 由
// 完成检测器判定，不在本层。
func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info().Msg("Agent 启动")

	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	workCtx, stopWork := context.WithCancel(ctx)
	defer func() {
		stopWork()
		a.workers.Wait()
	}()

	go a.watchStalls(workCtx)

	ticker := time.NewTicker(constants.ToolTimeoutQuick) // 定时兜底：错过的事件与外部写入的动作
	defer ticker.Stop()

	for {
		if err := a.loopFailure(a.dispatch(workCtx), "认领动作"); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			a.logger.Info().Msg("Agent 停止（context 取消）")
			return ctx.Err()
		case err := <-a.failures:
			if fatal := a.loopFailure(err, "执行动作"); fatal != nil {
				return fatal
			}
		case event := <-sub.Events():
			if event.Type != bus.EventActionProposed {
				continue
			}
		case <-a.slotFreed:
		case <-ticker.C:
		}
	}
}

// dispatch 把 open 动作按派发的先后派到空位上，每个动作一个协程执行。
//
// 先后由规则定，不要模型给动作打分（模型自评的优先级七成以上落在最高两档，等于没标）：
//  1. 用户指派的动作——人在运行中的纠偏，排在规划者排出的全部动作之前；
//  2. 规划者标了加急的那一个（Node.Urgent，一轮至多一个）；
//  3. 认领了线索的动作——深挖的边际收益高于再摸一个对象面；
//  4. 其余。
//
// 同一档里按创建先后（同一时刻创建的按 ID 定序，结果确定）。认领在执行协程里以 CAS 完成，
// 派发只占位防同一动作重复派发。
func (a *Agent) dispatch(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return nil // run 正在收尾，不再派发
	default:
	}
	open, err := a.graph.ListOpenActions(ctx, a.taskID)
	if err != nil {
		return fmt.Errorf("list open actions: %w", err)
	}
	claimants, err := a.graph.LeadClaimants(ctx, a.taskID)
	if err != nil {
		return fmt.Errorf("list lead claimants: %w", err)
	}
	sort.SliceStable(open, func(i, j int) bool {
		return dispatchOrder(open[i], open[j], claimants)
	})
	for _, action := range open {
		taken, full := a.reserveSlot(action.ID)
		if full {
			return nil
		}
		if !taken {
			continue // 已在执行
		}
		a.workers.Add(1)
		go func(act explorationgraph.Node) {
			defer a.workers.Done()
			defer a.releaseSlot(act.ID)
			if err := a.executeAction(ctx, act); err != nil {
				select {
				case a.failures <- fmt.Errorf("执行动作 %s: %w", act.ID, err):
				case <-ctx.Done():
				}
			}
		}(action)
	}
	return nil
}

// dispatchOrder 报告动作 a 是否排在 b 前面派发：先比档位（见 dispatchRank），同档比创建
// 先后，再比 ID。
func dispatchOrder(a, b explorationgraph.Node, claimants map[string]bool) bool {
	if ra, rb := dispatchRank(a, claimants), dispatchRank(b, claimants); ra != rb {
		return ra < rb
	}
	if byTime := a.CreatedAt.Compare(b.CreatedAt); byTime != 0 {
		return byTime < 0
	}
	return a.ID < b.ID
}

// dispatchRank 是派发先后的档位（小者先）：用户指派、加急、认领线索、其余。
func dispatchRank(action explorationgraph.Node, claimants map[string]bool) int {
	switch {
	case action.SourceType == explorationgraph.SourceUser:
		return 0
	case action.Urgent:
		return 1
	case claimants[action.ID]:
		return 2
	default:
		return 3
	}
}

// reserveSlot 为动作占一个执行位：taken 表示占位成功，full 表示已无空位。
func (a *Agent) reserveSlot(actionID string) (taken, full bool) {
	a.slotsMu.Lock()
	defer a.slotsMu.Unlock()
	if _, busy := a.slots[actionID]; busy {
		return false, false
	}
	if len(a.slots) >= a.maxParallel {
		return false, true
	}
	a.slots[actionID] = struct{}{}
	return true, false
}

// releaseSlot 释放执行位并唤醒派发补位。
func (a *Agent) releaseSlot(actionID string) {
	a.slotsMu.Lock()
	delete(a.slots, actionID)
	a.slotsMu.Unlock()
	select {
	case a.slotFreed <- struct{}{}:
	default:
	}
}

// loopFailure 处置一轮执行失败：模型配置错误（鉴权/权限/模型不存在）原样返回——
// 致命，Run 退出由认知循环终结任务（重试不会自愈）；其余失败记录后继续下一轮。
func (a *Agent) loopFailure(err error, phase string) error {
	if err == nil {
		return nil
	}
	if llm.IsConfigError(err) {
		a.logger.Error().Err(err).Str("phase", phase).Msg("模型配置错误，执行无法继续")
		return err
	}
	logx.ErrorUnlessCanceled(a.logger, err).Err(err).Str("phase", phase).Msg("执行失败（下一轮继续）")
	return nil
}

// executeAction 执行单个 Action
func (a *Agent) executeAction(
	ctx context.Context,
	action explorationgraph.Node,
) error {
	a.logger.Info().
		Str("action_id", action.ID).
		Bool("urgent", action.Urgent).
		Msg("开始执行 Action")

	ok, err := a.graph.ClaimAction(ctx, a.taskID, action.ID, a.agentRunID)
	if err != nil {
		return fmt.Errorf("认领动作: %w", err)
	}
	if !ok {
		a.logger.Info().Str("action_id", action.ID).Msg("动作已被认领或已结束，跳过")
		return nil
	}

	actionCtx, untrack := a.track(ctx, action.ID)
	outcome, execErr := a.executor.Execute(actionCtx, action)
	stall := stallCause(actionCtx)
	untrack()

	// 落图用独立短时 ctx：run 被打断时执行期 ctx 已取消，用它写图必然失败。
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	// 动作在执行途中被收回（否决、任务收口、被新 run 回收）：外部决定已落图，放弃本次
	// 结果，不改写动作状态，也不算执行失败。
	if errors.Is(execErr, ErrActionRevoked) {
		a.logger.Info().Str("action_id", action.ID).Err(execErr).Msg("动作已被收回，放弃本次执行结果")
		return nil
	}

	// run 被中断（暂停、停机、run 超时）而动作没做完：交还 open，由下一个 run 重新
	// 执行。中断不是失败——记成失败会让规划者以为此路不通。
	if execErr != nil && ctx.Err() != nil {
		if _, err := a.graph.ReleaseAction(markCtx, a.taskID, action.ID, a.agentRunID); err != nil {
			a.logger.Error().Err(err).Str("action_id", action.ID).Msg("交还中断的动作失败")
		}
		return nil
	}

	// 执行期遇基础设施瞬时故障（provider 限流 / 过载 / 网络抖动）：这不是对动作的判决，
	// 与「中断」同类——交还 open 待重试，不记 failed。记成失败会让规划者把可行的方向
	// 读作「此路不通」而弃掉。瞬时判定委托 llm.IsTransient（全系统唯一事实源）。看门狗
	// 判停摆的动作（stall）是真·卡死，不在此列，仍按失败结束。调用层的短暂故障已由重试
	// 装饰器与流式降级吸收，走到这里的是退避耗尽后仍未恢复的持续故障：交还而非记死，
	// 让方向在 provider 恢复后仍可原路继续。
	if execErr != nil && stall == nil && llm.IsTransient(execErr) {
		a.logger.Warn().Str("action_id", action.ID).Err(execErr).
			Msg("执行期 provider 瞬时故障，交还动作待重试（不记失败）")
		if _, err := a.graph.ReleaseAction(markCtx, a.taskID, action.ID, a.agentRunID); err != nil {
			a.logger.Error().Err(err).Str("action_id", action.ID).Msg("交还瞬时故障的动作失败")
		}
		return nil
	}

	// 结束以「仍由本 run 认领且仍 running」为前置：外部决定（否决、任务收口）或新 run
	// 的回收已改写状态时，迟到的结果不覆盖它们。看门狗判停摆的动作在这里按失败结束。
	to, reason := explorationgraph.StateCompleted, ""
	switch {
	case stall != nil:
		to, reason = explorationgraph.StateFailed, stall.Error()
	case execErr != nil:
		to, reason = explorationgraph.StateFailed, execErr.Error()
	case outcome == OutcomeExhausted:
		// 预算耗尽：有产出但没收敛，规划者与监察者据此区分「做完了」与「没做完」
		to = explorationgraph.StateExhausted
	}
	finished, err := a.graph.FinishAction(markCtx, a.taskID, action.ID, a.agentRunID, to, reason)
	if err != nil {
		a.logger.Error().Err(err).Str("action_id", action.ID).Msg("动作结束状态写入失败")
		return execErr
	}
	if !finished {
		a.logger.Warn().Str("action_id", action.ID).Msg("动作已被外部改写或回收，保留外部决定")
		return execErr
	}
	if execErr != nil {
		return execErr
	}

	a.logger.Info().Str("action_id", action.ID).Msg("Action 执行完成")

	return nil
}
