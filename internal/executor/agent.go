// Package executor 提供执行层的 Agent
//
// Agent 是持续运行的异步执行器，通过事件驱动响应 Action，生成 Observation
package executor

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/constants"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/logx"
)

// Agent 是事件驱动的执行器 Agent
//
// 职责：
// - 订阅 ActionProposed 事件（由 planner.Agent 发布）
// - 执行 Action 并生成 Observation（通过 Attempt）
// - 发布 ActionCompleted 事件
// - 完全异步，不阻塞任何调用方
type Agent struct {
	graph        *explorationgraph.Store
	executor     ActionExecutor
	eventBus     bus.EventBus
	logger       zerolog.Logger
	taskID       string
	agentRunID   string // 本 run 的 ID：认领动作时记下，结束与交还以此校验归属
	maxSteps     int
	maxParallel  int         // 并发执行的 action 上限（1=串行）
	completionCh chan Report // 任务完成通知通道

	// reportMu 保护共享 Report（并行执行 action 时计数并发递增）
	reportMu sync.Mutex

	// 看门狗：在途动作超过 stallTimeout 无进展即取消（见 watchdog.go）
	activity           *ActivityTracker
	stallTimeout       time.Duration
	stallCheckInterval time.Duration
	inFlightMu         sync.Mutex
	inFlight           map[string]inFlightAction
}

// AgentConfig 配置 Agent
type AgentConfig struct {
	TaskID       string
	AgentRunID   string // 本 run 的 ID（动作认领归属）
	Graph        *explorationgraph.Store
	Executor     ActionExecutor
	EventBus     bus.EventBus
	Logger       zerolog.Logger
	MaxSteps     int         // 最大执行步数（0 = 默认 1000）
	MaxParallel  int         // 并发执行 action 上限（0/1=串行）
	CompletionCh chan Report // 可选：任务完成时写入 Report

	// Activity 是动作进展记录（工具拦截器与执行引擎写入，看门狗读取）；nil 时自建。
	Activity *ActivityTracker
	// StallTimeout 是判定停摆的无进展时长（见 StallTimeout）；0 = 不启用看门狗。
	StallTimeout time.Duration
	// StallCheckInterval 是看门狗巡检周期（0 = 30 秒）。
	StallCheckInterval time.Duration
}

// NewAgent 创建 Agent
func NewAgent(cfg AgentConfig) *Agent {
	if cfg.MaxSteps == 0 {
		cfg.MaxSteps = 1000 // 默认最大步数
	}
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
		graph:        cfg.Graph,
		executor:     cfg.Executor,
		eventBus:     cfg.EventBus,
		logger:       cfg.Logger.With().Str("agent", "executor").Logger(),
		taskID:       cfg.TaskID,
		agentRunID:   cfg.AgentRunID,
		maxSteps:     cfg.MaxSteps,
		maxParallel:  cfg.MaxParallel,
		completionCh: cfg.CompletionCh,

		activity:           cfg.Activity,
		stallTimeout:       cfg.StallTimeout,
		stallCheckInterval: cfg.StallCheckInterval,
		inFlight:           map[string]inFlightAction{},
	}
}

// Run 启动事件循环（ctx 取消即停止）。
//
// 职责：
// - 监听事件总线上的 Action 状态变化
// - 认领并执行 open 动作
// - 发布 ActionCompleted 事件
func (a *Agent) Run(ctx context.Context) error {
	a.logger.Info().Msg("Agent 启动")

	// 订阅事件
	sub := a.eventBus.SubscribeTask(a.taskID)
	defer sub.Cancel()

	// 看门狗与执行并行运行：执行循环在动作跑完前不会返回。
	go a.watchStalls(ctx)

	// 执行状态
	var report Report

	// 立即检查一次待执行的 Action
	if err := a.loopFailure(a.processAvailableActions(ctx, &report), "初始 Action 处理"); err != nil {
		return err
	}

	// 事件循环
	ticker := time.NewTicker(constants.ToolTimeoutQuick) // 定期兜底检查
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			a.logger.Info().Msg("Agent 停止（context 取消）")
			report.StopReason = stopCanceled
			a.notifyCompletion(report)
			return ctx.Err()

		case event := <-sub.Events():
			a.logger.Debug().
				Str("event_type", string(event.Type)).
				Msg("收到事件")

			// 处理事件
			if err := a.loopFailure(a.handleEvent(ctx, event, &report), "事件处理（"+string(event.Type)+"）"); err != nil {
				return err
			}

			// 检查停止条件
			if a.shouldStop(&report) {
				a.logger.Info().
					Str("stop_reason", report.StopReason).
					Int("steps", report.Steps).
					Msg("Agent 达到停止条件")
				a.notifyCompletion(report)
				return nil
			}

		case <-ticker.C:
			// 定期兜底：检查是否有遗漏的 Action
			if err := a.loopFailure(a.processAvailableActions(ctx, &report), "定期检查 Action"); err != nil {
				return err
			}

			// 检查停止条件
			if a.shouldStop(&report) {
				a.logger.Info().
					Str("stop_reason", report.StopReason).
					Int("steps", report.Steps).
					Msg("Agent 达到停止条件（定期检查）")
				a.notifyCompletion(report)
				return nil
			}
		}
	}
}

// loopFailure 处置一轮执行失败：LLM 配置错误（鉴权/权限/模型不存在）原样返回——
// 致命，Run 退出由认知循环终结任务（重试不会自愈）；其余失败记录后继续下一轮。
func (a *Agent) loopFailure(err error, phase string) error {
	if err == nil {
		return nil
	}
	if llm.IsConfigError(err) {
		a.logger.Error().Err(err).Str("phase", phase).Msg("LLM 配置错误，执行无法继续")
		return err
	}
	logx.ErrorUnlessCanceled(a.logger, err).Err(err).Str("phase", phase).Msg("执行失败（下一轮继续）")
	return nil
}

// handleEvent 处理事件
func (a *Agent) handleEvent(ctx context.Context, event bus.Event, report *Report) error {
	switch event.Type {
	case bus.EventActionProposed:
		// Planner 提议了新 Action，立即处理
		a.logger.Info().
			Msg("收到 ActionProposed 事件，处理可执行 Action")
		return a.processAvailableActions(ctx, report)

	default:
		a.logger.Debug().
			Str("event_type", string(event.Type)).
			Msg("忽略事件（非 Executor 关注）")
		return nil
	}
}

// processAvailableActions 并发执行全部 open 动作（CAS 认领防重复执行）
func (a *Agent) processAvailableActions(ctx context.Context, report *Report) error {
	// 获取所有 open 状态的 Action
	openActions, err := a.graph.ListOpenActions(ctx, a.taskID)
	if err != nil {
		return fmt.Errorf("list open actions: %w", err)
	}

	if len(openActions) == 0 {
		a.logger.Debug().Msg("无待执行 Action")
		return nil
	}

	a.logger.Info().Int("open_count", len(openActions)).Msg("开始执行待执行动作")

	// 并发执行 open 动作：动作之间没有执行依赖（先后由增量规划表达），并发度由
	// 配置决定，不由 LLM 运行时决定。
	sem := make(chan struct{}, a.maxParallel)
	var wg sync.WaitGroup
	var firstErr error
	var errMu sync.Mutex

	for _, action := range openActions {
		if err := ctx.Err(); err != nil {
			break
		}
		wg.Add(1)
		go func(act explorationgraph.Node) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			if err := a.executeAction(ctx, act, report); err != nil {
				logx.ErrorUnlessCanceled(a.logger, err).
					Err(err).
					Str("action_id", act.ID).
					Msg("执行 Action 失败")
				// 单个失败不中止其他 Action；记录首个错误供上层感知
				errMu.Lock()
				if firstErr == nil {
					firstErr = err
				}
				errMu.Unlock()
			}
		}(action)
	}
	wg.Wait()

	return firstErr
}

// executeAction 执行单个 Action
func (a *Agent) executeAction(
	ctx context.Context,
	action explorationgraph.Node,
	report *Report,
) error {
	a.logger.Info().
		Str("action_id", action.ID).
		Str("priority", string(action.Priority)).
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
	attempts, outcome, execErr := a.executor.Execute(actionCtx, action)
	stall := stallCause(actionCtx)
	untrack()

	// 落图用独立短时 ctx：run 被打断时执行期 ctx 已取消，用它写图必然失败。
	markCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()

	// run 被中断（暂停、停机、run 超时）而动作没做完：交还 open，由下一个 run 重新
	// 执行。中断不是失败——记成失败会让规划者以为此路不通。
	if execErr != nil && ctx.Err() != nil {
		if _, err := a.graph.ReleaseAction(markCtx, a.taskID, action.ID, a.agentRunID); err != nil {
			a.logger.Error().Err(err).Str("action_id", action.ID).Msg("交还中断的动作失败")
		}
		return nil
	}

	// 结束以「仍由本 run 认领且仍 running」为前置：外部决定（monitor 中止）或新 run
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

	// 更新 report（并行执行下并发递增，加锁）
	a.reportMu.Lock()
	report.Steps++
	report.Attempts += len(attempts)
	a.reportMu.Unlock()

	// 发布所有 attempt.generated 事件（通知 evaluator）
	for _, attempt := range attempts {
		a.eventBus.PublishAttemptGenerated(a.taskID, action.ID, attempt)
	}

	// 最后发布 ActionCompleted 事件（确保 Evaluator 已收到所有 Attempt）
	a.eventBus.PublishActionCompleted(a.taskID, action.ID)

	a.logger.Info().
		Str("action_id", action.ID).
		Int("attempts_count", len(attempts)).
		Msg("Action 执行完成")

	return nil
}

// shouldStop 判断是否应停止
func (a *Agent) shouldStop(report *Report) bool {
	// 达到最大步数
	if a.maxSteps > 0 && report.Steps >= a.maxSteps {
		report.StopReason = stopMaxSteps
		return true
	}

	// 其余停止条件不在本层：「无新 Action 且图收敛」由 planner 直接通知、
	// completion detector 判定；「外部停止」走控制平面 terminate → detector.Abort。
	return false
}

// notifyCompletion 通知任务完成
func (a *Agent) notifyCompletion(report Report) {
	if a.completionCh != nil {
		select {
		case a.completionCh <- report:
			a.logger.Info().Msg("任务完成报告已发送")
		default:
			a.logger.Warn().Msg("任务完成报告发送失败（channel 已满）")
		}
	}
}
