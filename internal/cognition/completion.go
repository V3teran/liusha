// Package cognition 提供认知循环的完成检测机制
package cognition

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/knowledgegraph"
)

// CompletionDetector 检测任务完成状态
//
// 完成条件（任一触发）：
// 1. 所有 Action 执行完毕 + Planner 连续 N 轮未生成新 Action
// 2. 达到最大步数
// 3. 达到超时时间
// 4. 收到人工中止信号
type CompletionDetector struct {
	taskID string
	world  *knowledgegraph.Store
	bus    bus.Bus
	logger zerolog.Logger

	// 配置
	maxSteps              int           // 最大步数（0=无限制）
	idleRoundsThreshold   int           // Planner 空闲轮数阈值（连续 N 轮未生成 Action 则认为完成）
	checkInterval         time.Duration // 检查间隔
	gracePeriod           time.Duration // 优雅等待期（最后一个 Action 完成后的缓冲时间）
	minExecutionDuration  time.Duration // 最小执行时长（防止误判提前完成）

	// 运行时状态
	totalSteps      atomic.Int64
	promotedCount   atomic.Int64
	attemptCount    atomic.Int64
	idleRounds      atomic.Int64  // 连续空闲轮数
	lastActionTime  atomic.Int64  // 最后一次 Action 完成的时间戳（UnixMilli）
	startTime       time.Time
	manualAbort     atomic.Bool
	abortReason     atomic.Value  // string

	completionCh chan Result
}

// Result 是任务完成的最终报告
type Result struct {
	Steps     int    // 总执行步数
	Promoted  int    // 晋升的结果数量
	Attempts  int    // 总尝试次数
	StopWhy   string // 停止原因
	Duration  time.Duration
}

// Config 配置 CompletionDetector
type Config struct {
	TaskID                string
	World                 *knowledgegraph.Store
	Bus                   bus.Bus
	Logger                zerolog.Logger
	MaxSteps              int           // 默认 1000
	IdleRoundsThreshold   int           // 默认 3
	CheckInterval         time.Duration // 默认 5s
	GracePeriod           time.Duration // 默认 30s
	MinExecutionDuration  time.Duration // 默认 10s
}

// NewCompletionDetector 创建完成检测器
func NewCompletionDetector(cfg Config) *CompletionDetector {
	if cfg.MaxSteps <= 0 {
		cfg.MaxSteps = 1000
	}
	if cfg.IdleRoundsThreshold <= 0 {
		cfg.IdleRoundsThreshold = 3
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 5 * time.Second
	}
	if cfg.GracePeriod == 0 {
		cfg.GracePeriod = 30 * time.Second
	}
	if cfg.MinExecutionDuration == 0 {
		cfg.MinExecutionDuration = 10 * time.Second
	}

	return &CompletionDetector{
		taskID:                cfg.TaskID,
		world:                 cfg.World,
		bus:                   cfg.Bus,
		logger:                cfg.Logger.With().Str("component", "completion_detector").Logger(),
		maxSteps:              cfg.MaxSteps,
		idleRoundsThreshold:   cfg.IdleRoundsThreshold,
		checkInterval:         cfg.CheckInterval,
		gracePeriod:           cfg.GracePeriod,
		minExecutionDuration:  cfg.MinExecutionDuration,
		startTime:             time.Now(),
		completionCh:          make(chan Result, 1),
	}
}

// Start 启动完成检测（阻塞直到任务完成）
func (d *CompletionDetector) Start(ctx context.Context) Result {
	d.logger.Info().
		Str("task_id", d.taskID).
		Int("max_steps", d.maxSteps).
		Int("idle_threshold", d.idleRoundsThreshold).
		Msg("完成检测器启动")

	// 订阅事件
	events := d.bus.SubscribeTask(d.taskID)
	defer d.bus.UnsubscribeTask(d.taskID)

	// 定期检查
	ticker := time.NewTicker(d.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return d.makeResult("context_cancelled")

		case result := <-d.completionCh:
			// 其他 goroutine 触发完成
			return result

		case event := <-events:
			d.handleEvent(event)

			// 每次事件后检查是否完成
			if result, done := d.checkCompletion(); done {
				return result
			}

		case <-ticker.C:
			// 定期兜底检查
			if result, done := d.checkCompletion(); done {
				return result
			}
		}
	}
}

// handleEvent 处理事件并更新统计
func (d *CompletionDetector) handleEvent(event bus.Event) {
	switch event.Type {
	case bus.EventActionCompleted:
		d.totalSteps.Add(1)
		d.lastActionTime.Store(time.Now().UnixMilli())
		d.logger.Debug().
			Int64("total_steps", d.totalSteps.Load()).
			Msg("Action 完成")

	case bus.EventAttemptGenerated:
		d.attemptCount.Add(1)

	case bus.EventVerificationPassed:
		d.promotedCount.Add(1)
		d.logger.Debug().
			Int64("promoted", d.promotedCount.Load()).
			Msg("结果晋升")

	case bus.EventActionProposed:
		// Planner 生成了新 Action，重置空闲计数
		d.idleRounds.Store(0)
		d.logger.Debug().Msg("Planner 生成新 Action，重置空闲计数")

	case bus.EventManualGuidance:
		// 人工干预，可能是中止信号
		if reason, ok := event.Payload["abort_reason"].(string); ok && reason != "" {
			d.manualAbort.Store(true)
			d.abortReason.Store(reason)
			d.logger.Info().Str("reason", reason).Msg("收到人工中止信号")
		}
	}
}

// checkCompletion 检查是否满足完成条件
func (d *CompletionDetector) checkCompletion() (Result, bool) {
	// 1. 人工中止
	if d.manualAbort.Load() {
		reason := "manual_abort"
		if v := d.abortReason.Load(); v != nil {
			reason = v.(string)
		}
		return d.makeResult(reason), true
	}

	// 2. 达到最大步数
	steps := d.totalSteps.Load()
	if d.maxSteps > 0 && steps >= int64(d.maxSteps) {
		d.logger.Info().
			Int64("steps", steps).
			Int("max_steps", d.maxSteps).
			Msg("达到最大步数")
		return d.makeResult("max_steps_reached"), true
	}

	// 3. 检查是否已稳定（需同时满足）：
	//    a. 无 open 状态的 Action
	//    b. Planner 连续 N 轮空闲
	//    c. 最后一个 Action 完成后已过优雅等待期
	//    d. 运行时长超过最小执行时长
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	openActions, err := d.world.ListOpenActions(ctx, d.taskID)
	if err != nil {
		d.logger.Warn().Err(err).Msg("检查 open actions 失败")
		return Result{}, false
	}

	hasOpenActions := len(openActions) > 0

	// 如果还有 open Action，任务未完成
	if hasOpenActions {
		return Result{}, false
	}

	// 无 open Action，检查 Planner 是否空闲
	idleRounds := d.idleRounds.Load()
	d.idleRounds.Add(1) // 本轮检查计为一次空闲

	if idleRounds < int64(d.idleRoundsThreshold) {
		d.logger.Debug().
			Int64("idle_rounds", idleRounds).
			Int("threshold", d.idleRoundsThreshold).
			Msg("Planner 空闲轮数不足，继续等待")
		return Result{}, false
	}

	// 检查优雅等待期
	lastActionMillis := d.lastActionTime.Load()
	if lastActionMillis > 0 {
		elapsed := time.Since(time.UnixMilli(lastActionMillis))
		if elapsed < d.gracePeriod {
			d.logger.Debug().
				Dur("elapsed", elapsed).
				Dur("grace_period", d.gracePeriod).
				Msg("优雅等待期未满，继续等待")
			return Result{}, false
		}
	}

	// 检查最小执行时长（防止任务刚启动就误判完成）
	runDuration := time.Since(d.startTime)
	if runDuration < d.minExecutionDuration {
		d.logger.Debug().
			Dur("duration", runDuration).
			Dur("min_duration", d.minExecutionDuration).
			Msg("运行时长不足，继续等待")
		return Result{}, false
	}

	// 所有条件满足，任务自然完成
	d.logger.Info().
		Int64("steps", steps).
		Int64("idle_rounds", idleRounds).
		Dur("duration", runDuration).
		Msg("任务自然完成（无更多工作）")
	return d.makeResult("natural_completion"), true
}

// makeResult 构造最终报告
func (d *CompletionDetector) makeResult(stopWhy string) Result {
	return Result{
		Steps:    int(d.totalSteps.Load()),
		Promoted: int(d.promotedCount.Load()),
		Attempts: int(d.attemptCount.Load()),
		StopWhy:  stopWhy,
		Duration: time.Since(d.startTime),
	}
}

// Abort 手动中止任务
func (d *CompletionDetector) Abort(reason string) {
	d.manualAbort.Store(true)
	d.abortReason.Store(reason)
	d.logger.Info().Str("reason", reason).Msg("手动触发中止")
}

// GetStats 获取当前统计（非阻塞）
func (d *CompletionDetector) GetStats() Result {
	return d.makeResult("in_progress")
}
