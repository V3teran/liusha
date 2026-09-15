package orchestrator

import (
	"context"

	"github.com/rs/zerolog"
)

// MetricsCollector 编排器性能指标收集器
type MetricsCollector struct {
	logger zerolog.Logger
}

// NewMetricsCollector 创建指标收集器
func NewMetricsCollector(logger zerolog.Logger) *MetricsCollector {
	return &MetricsCollector{
		logger: logger.With().Str("component", "orchestrator_metrics").Logger(),
	}
}

// CheckActionScale 检查任务的 Action 规模，当超过阈值时记录告警
//
// 阈值设计：
// - 100+ actions: 记录信息日志，正常规模
// - 500+ actions: 记录警告日志，建议评估是否需要专用 DAG 引擎
//
// 当前架构的依赖解析为 O(n)，但在 action 数量极大时可能需要：
// 1. 专用拓扑排序算法
// 2. 更复杂的调度策略（优先级队列、资源感知）
// 3. 分层执行优化
func (m *MetricsCollector) CheckActionScale(ctx context.Context, taskID string, count int) {
	if count > 500 {
		m.logger.Warn().
			Str("task_id", taskID).
			Int("action_count", count).
			Msg("⚠️  Action 规模超过性能评估阈值（500），建议评估是否需要引入专用 DAG 引擎")
	} else if count > 100 {
		m.logger.Info().
			Str("task_id", taskID).
			Int("action_count", count).
			Msg("Action 规模监控：接近性能评估阈值")
	}
}

// RecordDependencyResolutionTime 记录依赖解析耗时
//
// 当耗时超过阈值时发出告警，表明当前规模下手写逻辑可能成为瓶颈
func (m *MetricsCollector) RecordDependencyResolutionTime(ctx context.Context, taskID string, durationMs int64) {
	if durationMs > 2000 {
		m.logger.Warn().
			Str("task_id", taskID).
			Int64("duration_ms", durationMs).
			Msg("⚠️  依赖解析耗时超过 2 秒，可能存在性能问题")
	} else if durationMs > 1000 {
		m.logger.Info().
			Str("task_id", taskID).
			Int64("duration_ms", durationMs).
			Msg("依赖解析耗时监控：接近告警阈值")
	}
}
