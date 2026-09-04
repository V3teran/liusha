package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/rs/zerolog"
)

// MetricsExporter 暴露 Sandbox 监控指标
type MetricsExporter struct {
	mgr    *RefCountManager
	logger zerolog.Logger
}

// NewMetricsExporter 创建指标导出器
func NewMetricsExporter(mgr *RefCountManager, logger zerolog.Logger) *MetricsExporter {
	return &MetricsExporter{
		mgr:    mgr,
		logger: logger.With().Str("component", "sandbox_metrics").Logger(),
	}
}

// Metrics 容器指标快照
type Metrics struct {
	Timestamp       time.Time         `json:"timestamp"`
	TotalContainers int               `json:"total_containers"`
	ActiveCount     int               `json:"active_count"`
	IdleCount       int               `json:"idle_count"`
	OldestIdleAge   string            `json:"oldest_idle_age"`
	Containers      []ContainerMetric `json:"containers,omitempty"`
}

// ContainerMetric 单个容器的指标
type ContainerMetric struct {
	AssignmentID string `json:"assignment_id"`
	RefCount     int    `json:"ref_count"`
	LastActivity string `json:"last_activity"`
	IdleTime     string `json:"idle_time"`
}

// CollectMetrics 收集当前指标
func (e *MetricsExporter) CollectMetrics(includeDetails bool) Metrics {
	stats := e.mgr.Stats()

	metrics := Metrics{
		Timestamp:       time.Now(),
		TotalContainers: stats.TotalContainers,
		ActiveCount:     stats.ActiveCount,
		IdleCount:       stats.IdleCount,
		OldestIdleAge:   stats.OldestIdleAge.String(),
	}

	if includeDetails {
		metrics.Containers = e.collectContainerDetails()
	}

	return metrics
}

// collectContainerDetails 收集每个容器的详细信息
func (e *MetricsExporter) collectContainerDetails() []ContainerMetric {
	e.mgr.mu.RLock()
	defer e.mgr.mu.RUnlock()

	e.mgr.poolMgr.mu.RLock()
	defer e.mgr.poolMgr.mu.RUnlock()

	var containers []ContainerMetric
	now := time.Now()

	for assignmentID, pool := range e.mgr.poolMgr.pools {
		lastActivity, ok := e.mgr.lastActivity[assignmentID]
		if !ok {
			lastActivity = now
		}

		containers = append(containers, ContainerMetric{
			AssignmentID: assignmentID,
			RefCount:     pool.refCount,
			LastActivity: lastActivity.Format(time.RFC3339),
			IdleTime:     now.Sub(lastActivity).String(),
		})
	}

	return containers
}

// ServeHTTP 实现 http.Handler 接口
func (e *MetricsExporter) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	includeDetails := r.URL.Query().Get("details") == "true"

	metrics := e.CollectMetrics(includeDetails)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(metrics); err != nil {
		e.logger.Error().Err(err).Msg("failed to encode metrics")
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// LogMetrics 定期打印指标到日志
func (e *MetricsExporter) LogMetrics(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return

		case <-ticker.C:
			metrics := e.CollectMetrics(false)
			e.logger.Info().
				Int("total", metrics.TotalContainers).
				Int("active", metrics.ActiveCount).
				Int("idle", metrics.IdleCount).
				Str("oldest_idle", metrics.OldestIdleAge).
				Msg("sandbox metrics")
		}
	}
}

// HealthCheck 检查容器池健康状态
func (e *MetricsExporter) HealthCheck() error {
	stats := e.mgr.Stats()

	// 检查是否有过多僵尸容器
	if stats.IdleCount > 10 {
		e.logger.Warn().
			Int("idle_count", stats.IdleCount).
			Msg("too many idle containers")
	}

	// 检查最老的空闲容器是否超时
	maxIdleAge := 3 * time.Hour
	if stats.OldestIdleAge > maxIdleAge {
		e.logger.Warn().
			Dur("oldest_idle_age", stats.OldestIdleAge).
			Dur("max_idle_age", maxIdleAge).
			Msg("stale container detected")
	}

	return nil
}
