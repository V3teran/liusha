// Package cognition 实现认知循环：四 Agent（planner/executor/evaluator/monitor）
// 的装配与驱动（loop.go）、启停生命周期（lifecycle.go）、控制平面消费（control.go）、
// 工具遥测（telemetry.go）与完成检测（本文件）。
package cognition

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
)

// CompletionDetector 判定一次 run 何时结束。按优先级：
//  1. 人工中止（终止、暂停退出共用此通道）
//  2. 规划者宣告收敛（Converge，直接调用）
//  3. 动作预算用尽（执行过的动作数达到上限；0 = 不限）
//
// 另有两条旁路立即结束：agent 致命失败（Fail）与 run 被打断（ctx 取消）。动作预算
// 与报告统计都从图上计数，不依赖事件总线。
type CompletionDetector struct {
	taskID string
	graph  *explorationgraph.Store
	logger zerolog.Logger

	maxActions    int           // 动作预算（0 = 不限）
	checkInterval time.Duration // 兜底检查间隔

	startTime time.Time
	// abortReason 非 nil 即已人工中止，指向中止原因。标志与原因必须是同一个原子值：
	// 拆成「先置标志、后存原因」两步时，并发的兜底检查会落在两步之间，看到已中止却
	// 读不到原因，回落兜底值把调用方原因吞掉。
	abortReason atomic.Pointer[string]
	// converged 非 nil 即规划者已宣告收敛（无新方向且图上无未完结工作）。
	converged atomic.Pointer[convergence]
	// wake 让控制信号（收敛）立即触发完成判定，不等定时兜底检查。
	wake chan struct{}

	completionCh chan Result
}

// convergence 是规划者的一次收敛宣告：原因与所依据的目标。
type convergence struct {
	reason string
	basis  explorationgraph.GoalBasis
}

// Result 是一次 run 的结局与图统计。
type Result struct {
	Actions    int    // 执行过的动作数
	Results    int    // 坐实的结果数
	Hypotheses int    // 提出的假设数
	StopReason string // 停止原因
	Duration   time.Duration
	Err        error // 致命失败（StopReasonAgentFailed）的原因；其余停止为 nil
	// GoalBasis 是收敛所依据的目标（仅 StopReasonConverged 时有值）。
	GoalBasis explorationgraph.GoalBasis
}

// Config 配置 CompletionDetector
type Config struct {
	TaskID        string
	Graph         *explorationgraph.Store
	Logger        zerolog.Logger
	MaxActions    int           // 动作预算（0 = 不限）
	CheckInterval time.Duration // 默认 5s
}

// NewCompletionDetector 创建完成检测器
func NewCompletionDetector(cfg Config) *CompletionDetector {
	if cfg.MaxActions < 0 {
		cfg.MaxActions = 0
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 5 * time.Second
	}
	return &CompletionDetector{
		taskID:        cfg.TaskID,
		graph:         cfg.Graph,
		logger:        cfg.Logger.With().Str("component", "completion_detector").Logger(),
		maxActions:    cfg.MaxActions,
		checkInterval: cfg.CheckInterval,
		startTime:     time.Now(),
		wake:          make(chan struct{}, 1),
		completionCh:  make(chan Result, 1),
	}
}

// Start 启动完成检测（阻塞直到本 run 结束）
func (d *CompletionDetector) Start(ctx context.Context) Result {
	d.logger.Info().Int("max_actions", d.maxActions).Msg("完成检测器启动")

	ticker := time.NewTicker(d.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return d.makeResult(StopReasonCancelled)

		case result := <-d.completionCh:
			return result

		case <-d.wake:
			if result, done := d.checkCompletion(); done {
				return result
			}

		case <-ticker.C:
			if result, done := d.checkCompletion(); done {
				return result
			}
		}
	}
}

// checkCompletion 检查是否满足完成条件
func (d *CompletionDetector) checkCompletion() (Result, bool) {
	// 1. 人工中止（terminate / 暂停退出共用此通道，优先级最高）
	if r := d.abortReason.Load(); r != nil {
		reason := *r
		if reason == "" {
			reason = "manual_abort"
		}
		return d.makeResult(reason), true
	}

	// 2. 任务收敛（规划者宣告）
	if c := d.converged.Load(); c != nil {
		d.logger.Info().Str("reason", c.reason).Msg("任务收敛，停止探索")
		res := d.makeResult(StopReasonConverged)
		res.GoalBasis = c.basis
		return res, true
	}

	// 3. 动作预算
	if d.maxActions > 0 {
		st, err := d.stats()
		if err != nil {
			d.logger.Warn().Err(err).Msg("统计动作数失败，本轮不判预算")
			return Result{}, false
		}
		if st.ExecutedActions >= d.maxActions {
			d.logger.Info().Int("actions", st.ExecutedActions).Int("max_actions", d.maxActions).Msg("动作预算用尽")
			return d.makeResult(StopReasonActionBudget), true
		}
	}
	return Result{}, false
}

// makeResult 构造报告：统计从图上读（run 被打断时 ctx 已取消，用独立短时 ctx）。
func (d *CompletionDetector) makeResult(stopWhy string) Result {
	res := Result{StopReason: stopWhy, Duration: time.Since(d.startTime)}
	st, err := d.stats()
	if err != nil {
		d.logger.Warn().Err(err).Msg("统计任务图失败，报告统计为 0")
		return res
	}
	res.Actions, res.Results, res.Hypotheses = st.ExecutedActions, st.Results, st.Hypotheses
	return res
}

func (d *CompletionDetector) stats() (explorationgraph.TaskStats, error) {
	if d.graph == nil {
		return explorationgraph.TaskStats{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return d.graph.Stats(ctx, d.taskID)
}

// StopReasonCancelled 是 run 被打断（handler ctx 取消：停机、租约丢失、run 超时）的停止原因。
const StopReasonCancelled = "context_cancelled"

// StopReasonActionBudget 是动作预算用尽的停止原因。
const StopReasonActionBudget = "action_budget_reached"

// StopReasonConverged 是规划者宣告收敛（无新方向且图上无未完结工作）的停止原因。
const StopReasonConverged = "task_converged"

// Converge 由规划者在「无新方向且图上无未完结工作」时直接调用，带上收敛所依据的目标。
// 收敛决定任务结束，是控制信号：直接调用不会丢失，事件总线满即丢弃的语义不适合它。
func (d *CompletionDetector) Converge(reason string, basis explorationgraph.GoalBasis) {
	d.converged.Store(&convergence{reason: reason, basis: basis})
	select {
	case d.wake <- struct{}{}:
	default:
	}
}

// StopReasonAgentFailed 是 agent 致命失败（如 LLM 配置错误）终结任务的停止原因。
const StopReasonAgentFailed = "agent_failed"

// Fail 以致命错误终结检测循环（只取第一个：多个 agent 同因失败时不阻塞）。
func (d *CompletionDetector) Fail(err error) {
	res := d.makeResult(StopReasonAgentFailed)
	res.Err = err
	select {
	case d.completionCh <- res:
		d.logger.Error().Err(err).Msg("agent 致命失败，终止任务")
	default:
	}
}

// Abort 手动中止任务
func (d *CompletionDetector) Abort(reason string) {
	d.abortReason.Store(&reason)
	d.logger.Info().Str("reason", reason).Msg("手动触发中止")
}
