package metrics

import (
	"context"
	"fmt"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// MetricsCollector 收集和计算任务的时间序列指标
//
// 用于 Monitor 判断：
// - "每小时 action 数 <3" → 探索停滞
// - "每小时 result 数下降" → 方向错误
// - "result/action 比率 <0.1" → 资源耗尽
type MetricsCollector struct {
	graph  *explorationgraph.Store
	taskID string
	logger zerolog.Logger

	// 滑动窗口
	actionWindow *SlidingWindow // 每小时 action 数
	resultWindow *SlidingWindow // 每小时 result 数
	ratioWindow  *SlidingWindow // result/action 比率

	// 上次更新的时间戳（避免重复计数）
	lastActionTimestamp time.Time
	lastResultTimestamp time.Time
}

// NewMetricsCollector 创建指标收集器
//
// 默认窗口大小：1 小时，桶大小：10 分钟
func NewMetricsCollector(graph *explorationgraph.Store, taskID string, logger zerolog.Logger) *MetricsCollector {
	bucketSize := 10 * time.Minute
	windowSize := 1 * time.Hour

	return &MetricsCollector{
		graph:               graph,
		taskID:              taskID,
		logger:              logger,
		actionWindow:        NewSlidingWindow(bucketSize, windowSize),
		resultWindow:        NewSlidingWindow(bucketSize, windowSize),
		ratioWindow:         NewSlidingWindow(bucketSize, windowSize),
		lastActionTimestamp: time.Time{},
		lastResultTimestamp: time.Time{},
	}
}

// Update 更新指标（从探索图查询最新数据）
//
// 应该在每次 Monitor 评估前调用，收集最新的 action/result。
func (c *MetricsCollector) Update(ctx context.Context) error {
	// 1. 统计最近创建的 actions
	if err := c.updateActions(ctx); err != nil {
		return fmt.Errorf("update actions: %w", err)
	}

	// 2. 统计最近创建的 results
	if err := c.updateResults(ctx); err != nil {
		return fmt.Errorf("update results: %w", err)
	}

	// 3. 计算 result/action 比率
	c.updateRatio()

	return nil
}

// updateActions 统计最近创建的 actions
func (c *MetricsCollector) updateActions(ctx context.Context) error {
	// 查询所有 actions
	actions, err := c.graph.ListNodesByKind(ctx, c.taskID, core.KindAction)
	if err != nil {
		return err
	}

	// 统计自上次更新以来的新 actions
	newCount := 0
	latestTimestamp := c.lastActionTimestamp

	for _, action := range actions {
		if action.CreatedAt.After(c.lastActionTimestamp) {
			newCount++
			// 找到最新的时间戳
			if action.CreatedAt.After(latestTimestamp) {
				latestTimestamp = action.CreatedAt
			}
		}
	}

	// 更新最新时间戳
	if newCount > 0 {
		c.lastActionTimestamp = latestTimestamp

		// 添加到滑动窗口
		for i := 0; i < newCount; i++ {
			c.actionWindow.Add(1)
		}

		c.logger.Debug().
			Int("new_actions", newCount).
			Msg("更新 action 指标")
	}

	return nil
}

// updateResults 统计最近创建的 results
func (c *MetricsCollector) updateResults(ctx context.Context) error {
	// 查询所有 results
	results, err := c.graph.ListNodesByKind(ctx, c.taskID, core.KindResult)
	if err != nil {
		return err
	}

	// 统计自上次更新以来的新 results
	newCount := 0
	latestTimestamp := c.lastResultTimestamp

	for _, result := range results {
		if result.CreatedAt.After(c.lastResultTimestamp) {
			newCount++
			// 找到最新的时间戳
			if result.CreatedAt.After(latestTimestamp) {
				latestTimestamp = result.CreatedAt
			}
		}
	}

	// 更新最新时间戳
	if newCount > 0 {
		c.lastResultTimestamp = latestTimestamp

		// 添加到滑动窗口
		for i := 0; i < newCount; i++ {
			c.resultWindow.Add(1)
		}

		c.logger.Debug().
			Int("new_results", newCount).
			Msg("更新 result 指标")
	}

	return nil
}

// updateRatio 计算并更新 result/action 比率
func (c *MetricsCollector) updateRatio() {
	actionRate := c.actionWindow.Rate()
	resultRate := c.resultWindow.Rate()

	if actionRate > 0 {
		ratio := resultRate / actionRate
		c.ratioWindow.Add(ratio)

		c.logger.Debug().
			Float64("action_rate", actionRate).
			Float64("result_rate", resultRate).
			Float64("ratio", ratio).
			Msg("更新 ratio 指标")
	}
}

// GetMetrics 获取当前指标快照
func (c *MetricsCollector) GetMetrics() Metrics {
	return Metrics{
		ActionRatePerHour:    c.actionWindow.Rate(),
		ResultRatePerHour:    c.resultWindow.Rate(),
		AvgResultActionRatio: c.ratioWindow.Average(),
	}
}

// Metrics 是指标快照
type Metrics struct {
	ActionRatePerHour    float64 `json:"action_rate_per_hour"`     // 每小时 action 数
	ResultRatePerHour    float64 `json:"result_rate_per_hour"`     // 每小时 result 数
	AvgResultActionRatio float64 `json:"avg_result_action_ratio"`  // 平均 result/action 比率
}
