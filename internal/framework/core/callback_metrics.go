package core

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// MetricsCallback 是指标收集 Callback 实现。
// 统计执行次数、成功率、耗时等指标。
type MetricsCallback struct {
	NoopCallback

	// Agent 指标
	agentStartCount atomic.Int64
	agentEndCount   atomic.Int64
	agentErrorCount atomic.Int64

	// Tool 指标
	toolStartCount atomic.Int64
	toolEndCount   atomic.Int64
	toolErrorCount atomic.Int64
	toolDurations  sync.Map // tool_name -> []time.Duration

	// LLM 指标
	llmStartCount   atomic.Int64
	llmEndCount     atomic.Int64
	llmErrorCount   atomic.Int64
	llmTotalInTokens  atomic.Int64
	llmTotalOutTokens atomic.Int64
	llmCachedTokens   atomic.Int64
	llmDurations      []time.Duration
	llmDurationsLock  sync.Mutex
}

// NewMetricsCallback 创建指标收集 Callback。
func NewMetricsCallback() *MetricsCallback {
	return &MetricsCallback{
		llmDurations: make([]time.Duration, 0, 100),
	}
}

// OnAgentStart Agent 启动计数
func (m *MetricsCallback) OnAgentStart(ctx context.Context, event AgentStartEvent) {
	m.agentStartCount.Add(1)
}

// OnAgentEnd Agent 结束计数
func (m *MetricsCallback) OnAgentEnd(ctx context.Context, event AgentEndEvent) {
	m.agentEndCount.Add(1)
	if event.Error != nil {
		m.agentErrorCount.Add(1)
	}
}

// OnToolStart Tool 启动计数
func (m *MetricsCallback) OnToolStart(ctx context.Context, event ToolStartEvent) {
	m.toolStartCount.Add(1)
}

// OnToolEnd Tool 结束计数和耗时统计
func (m *MetricsCallback) OnToolEnd(ctx context.Context, event ToolEndEvent) {
	m.toolEndCount.Add(1)
	if event.Error != nil {
		m.toolErrorCount.Add(1)
	}

	// 记录耗时
	durationsVal, _ := m.toolDurations.LoadOrStore(event.ToolName, &sync.Mutex{})
	durationsLock := durationsVal.(*sync.Mutex)

	durationsLock.Lock()
	listVal, _ := m.toolDurations.LoadOrStore(event.ToolName+"_list", []time.Duration{})
	list := listVal.([]time.Duration)
	list = append(list, event.Duration)
	m.toolDurations.Store(event.ToolName+"_list", list)
	durationsLock.Unlock()
}

// OnLLMStart LLM 启动计数
func (m *MetricsCallback) OnLLMStart(ctx context.Context, event LLMStartEvent) {
	m.llmStartCount.Add(1)
}

// OnLLMEnd LLM 结束计数和 token 统计
func (m *MetricsCallback) OnLLMEnd(ctx context.Context, event LLMEndEvent) {
	m.llmEndCount.Add(1)
	if event.Error != nil {
		m.llmErrorCount.Add(1)
		return
	}

	// Token 统计
	m.llmTotalInTokens.Add(int64(event.Response.Usage.InTokens))
	m.llmTotalOutTokens.Add(int64(event.Response.Usage.OutTokens))
	m.llmCachedTokens.Add(int64(event.Response.Usage.CachedTokens))

	// 耗时统计
	m.llmDurationsLock.Lock()
	m.llmDurations = append(m.llmDurations, event.Duration)
	m.llmDurationsLock.Unlock()
}

// ─────────────────────────────────────────────
//  指标读取方法
// ─────────────────────────────────────────────

// Metrics 返回所有指标快照。
type Metrics struct {
	// Agent 指标
	AgentStartCount int64
	AgentEndCount   int64
	AgentErrorCount int64
	AgentErrorRate  float64

	// Tool 指标
	ToolStartCount int64
	ToolEndCount   int64
	ToolErrorCount int64
	ToolErrorRate  float64
	ToolAvgDuration time.Duration

	// LLM 指标
	LLMStartCount     int64
	LLMEndCount       int64
	LLMErrorCount     int64
	LLMErrorRate      float64
	LLMTotalInTokens  int64
	LLMTotalOutTokens int64
	LLMCachedTokens   int64
	LLMAvgDuration    time.Duration
}

// GetMetrics 获取指标快照。
func (m *MetricsCallback) GetMetrics() Metrics {
	metrics := Metrics{
		AgentStartCount: m.agentStartCount.Load(),
		AgentEndCount:   m.agentEndCount.Load(),
		AgentErrorCount: m.agentErrorCount.Load(),

		ToolStartCount: m.toolStartCount.Load(),
		ToolEndCount:   m.toolEndCount.Load(),
		ToolErrorCount: m.toolErrorCount.Load(),

		LLMStartCount:     m.llmStartCount.Load(),
		LLMEndCount:       m.llmEndCount.Load(),
		LLMErrorCount:     m.llmErrorCount.Load(),
		LLMTotalInTokens:  m.llmTotalInTokens.Load(),
		LLMTotalOutTokens: m.llmTotalOutTokens.Load(),
		LLMCachedTokens:   m.llmCachedTokens.Load(),
	}

	// 计算错误率
	if metrics.AgentEndCount > 0 {
		metrics.AgentErrorRate = float64(metrics.AgentErrorCount) / float64(metrics.AgentEndCount)
	}
	if metrics.ToolEndCount > 0 {
		metrics.ToolErrorRate = float64(metrics.ToolErrorCount) / float64(metrics.ToolEndCount)
	}
	if metrics.LLMEndCount > 0 {
		metrics.LLMErrorRate = float64(metrics.LLMErrorCount) / float64(metrics.LLMEndCount)
	}

	// 计算平均耗时
	m.llmDurationsLock.Lock()
	if len(m.llmDurations) > 0 {
		var total time.Duration
		for _, d := range m.llmDurations {
			total += d
		}
		metrics.LLMAvgDuration = total / time.Duration(len(m.llmDurations))
	}
	m.llmDurationsLock.Unlock()

	return metrics
}

// Reset 重置所有指标。
func (m *MetricsCallback) Reset() {
	m.agentStartCount.Store(0)
	m.agentEndCount.Store(0)
	m.agentErrorCount.Store(0)

	m.toolStartCount.Store(0)
	m.toolEndCount.Store(0)
	m.toolErrorCount.Store(0)
	m.toolDurations = sync.Map{}

	m.llmStartCount.Store(0)
	m.llmEndCount.Store(0)
	m.llmErrorCount.Store(0)
	m.llmTotalInTokens.Store(0)
	m.llmTotalOutTokens.Store(0)
	m.llmCachedTokens.Store(0)

	m.llmDurationsLock.Lock()
	m.llmDurations = make([]time.Duration, 0, 100)
	m.llmDurationsLock.Unlock()
}
