// Package cognition 提供认知循环的完成检测机制
package cognition

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// CompletionDetector 检测任务完成状态
//
// 完成条件（任一触发）：
// 1. 达到最大步数（可选，默认无限制）
// 2. 收到人工中止信号
//
// 注意：取消了"空闲轮数自动停止"机制，改为持续探索模式
// 任务会持续运行直到用户手动停止或达到资源限制
type CompletionDetector struct {
	taskID string
	world  *explorationgraph.Store
	bus    bus.Bus
	logger zerolog.Logger

	// 配置
	maxSteps     int           // 最大步数（0=无限制）
	checkInterval time.Duration // 检查间隔

	// 运行时状态
	totalSteps    atomic.Int64
	promotedCount atomic.Int64
	attemptCount  atomic.Int64
	startTime     time.Time
	manualAbort   atomic.Bool
	abortReason   atomic.Value // string

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
	TaskID        string
	World         *explorationgraph.Store
	Bus           bus.Bus
	Logger        zerolog.Logger
	MaxSteps      int           // 默认 0（无限制）
	CheckInterval time.Duration // 默认 5s
}

// NewCompletionDetector 创建完成检测器
func NewCompletionDetector(cfg Config) *CompletionDetector {
	// 默认无限制步数，改为持续探索模式
	if cfg.MaxSteps < 0 {
		cfg.MaxSteps = 0
	}
	if cfg.CheckInterval == 0 {
		cfg.CheckInterval = 5 * time.Second
	}

	return &CompletionDetector{
		taskID:        cfg.TaskID,
		world:         cfg.World,
		bus:           cfg.Bus,
		logger:        cfg.Logger.With().Str("component", "completion_detector").Logger(),
		maxSteps:      cfg.MaxSteps,
		checkInterval: cfg.CheckInterval,
		startTime:     time.Now(),
		completionCh:  make(chan Result, 1),
	}
}

// Start 启动完成检测（阻塞直到任务完成）
func (d *CompletionDetector) Start(ctx context.Context) Result {
	d.logger.Info().
		Str("task_id", d.taskID).
		Int("max_steps", d.maxSteps).
		Msg("完成检测器启动（持续探索模式）")

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

	// 2. 达到最大步数（如果设置了）
	if d.maxSteps > 0 {
		steps := d.totalSteps.Load()
		if steps >= int64(d.maxSteps) {
			d.logger.Info().
				Int64("steps", steps).
				Int("max_steps", d.maxSteps).
				Msg("达到最大步数")
			return d.makeResult("max_steps_reached"), true
		}
	}

	// 持续探索模式：不再基于空闲轮数或优雅等待期自动停止
	// 任务会一直运行，直到用户手动停止或达到资源限制
	return Result{}, false
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
