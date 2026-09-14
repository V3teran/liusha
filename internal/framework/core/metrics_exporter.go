package core

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsExporter 是 Prometheus 指标导出器。
type MetricsExporter struct {
	registry *prometheus.Registry
	mu       sync.RWMutex

	// Agent 指标
	agentStartTotal   *prometheus.CounterVec
	agentEndTotal     *prometheus.CounterVec
	agentDuration     *prometheus.HistogramVec
	agentErrorTotal   *prometheus.CounterVec

	// LLM 指标
	llmCallTotal      *prometheus.CounterVec
	llmDuration       *prometheus.HistogramVec
	llmErrorTotal     *prometheus.CounterVec
	llmTokensTotal    *prometheus.CounterVec
	llmCachedTokens   *prometheus.CounterVec

	// Tool 指标
	toolCallTotal     *prometheus.CounterVec
	toolDuration      *prometheus.HistogramVec
	toolErrorTotal    *prometheus.CounterVec

	// Node 指标
	nodeExecutionTotal *prometheus.CounterVec
	nodeDuration       *prometheus.HistogramVec
	nodeErrorTotal     *prometheus.CounterVec

	// 系统指标
	activeAgents      prometheus.Gauge
	queuedTasks       prometheus.Gauge
}

// NewMetricsExporter 创建 Prometheus 指标导出器。
func NewMetricsExporter(namespace string) *MetricsExporter {
	registry := prometheus.NewRegistry()

	exporter := &MetricsExporter{
		registry: registry,

		// Agent 指标
		agentStartTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "agent_start_total",
				Help:      "Agent 启动总次数",
			},
			[]string{"agent_name"},
		),
		agentEndTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "agent_end_total",
				Help:      "Agent 结束总次数",
			},
			[]string{"agent_name", "status"},
		),
		agentDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "agent_duration_seconds",
				Help:      "Agent 执行耗时分布（秒）",
				Buckets:   []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10},
			},
			[]string{"agent_name"},
		),
		agentErrorTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "agent_error_total",
				Help:      "Agent 错误总次数",
			},
			[]string{"agent_name"},
		),

		// LLM 指标
		llmCallTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "llm_call_total",
				Help:      "LLM 调用总次数",
			},
			[]string{"provider", "model"},
		),
		llmDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "llm_duration_seconds",
				Help:      "LLM 调用耗时分布（秒）",
				Buckets:   []float64{.1, .25, .5, 1, 2.5, 5, 10, 30, 60},
			},
			[]string{"provider", "model"},
		),
		llmErrorTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "llm_error_total",
				Help:      "LLM 调用错误总次数",
			},
			[]string{"provider", "model"},
		),
		llmTokensTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "llm_tokens_total",
				Help:      "LLM Token 使用总量",
			},
			[]string{"provider", "model", "type"},
		),
		llmCachedTokens: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "llm_cached_tokens_total",
				Help:      "LLM 缓存 Token 总量",
			},
			[]string{"provider", "model"},
		),

		// Tool 指标
		toolCallTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "tool_call_total",
				Help:      "Tool 调用总次数",
			},
			[]string{"tool_name"},
		),
		toolDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "tool_duration_seconds",
				Help:      "Tool 执行耗时分布（秒）",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"tool_name"},
		),
		toolErrorTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "tool_error_total",
				Help:      "Tool 执行错误总次数",
			},
			[]string{"tool_name"},
		),

		// Node 指标
		nodeExecutionTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "node_execution_total",
				Help:      "节点执行总次数",
			},
			[]string{"node_type"},
		),
		nodeDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "node_duration_seconds",
				Help:      "节点执行耗时分布（秒）",
				Buckets:   []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
			},
			[]string{"node_type"},
		),
		nodeErrorTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "node_error_total",
				Help:      "节点执行错误总次数",
			},
			[]string{"node_type"},
		),

		// 系统指标
		activeAgents: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "active_agents",
				Help:      "当前活跃 Agent 数量",
			},
		),
		queuedTasks: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "queued_tasks",
				Help:      "队列中等待的任务数量",
			},
		),
	}

	// 注册所有指标
	exporter.registerMetrics()

	return exporter
}

// registerMetrics 注册所有指标到 registry。
func (e *MetricsExporter) registerMetrics() {
	e.registry.MustRegister(
		e.agentStartTotal,
		e.agentEndTotal,
		e.agentDuration,
		e.agentErrorTotal,
		e.llmCallTotal,
		e.llmDuration,
		e.llmErrorTotal,
		e.llmTokensTotal,
		e.llmCachedTokens,
		e.toolCallTotal,
		e.toolDuration,
		e.toolErrorTotal,
		e.nodeExecutionTotal,
		e.nodeDuration,
		e.nodeErrorTotal,
		e.activeAgents,
		e.queuedTasks,
	)
}

// Handler 返回 HTTP 处理器，用于 Prometheus 抓取。
func (e *MetricsExporter) Handler() http.Handler {
	return promhttp.HandlerFor(e.registry, promhttp.HandlerOpts{
		EnableOpenMetrics: true,
	})
}

// ─────────────────────────────────────────────
//  Callback 集成
// ─────────────────────────────────────────────

// PrometheusCallback 是 Prometheus 指标收集的 Callback 实现。
type PrometheusCallback struct {
	NoopCallback
	exporter *MetricsExporter
}

// NewPrometheusCallback 创建 Prometheus Callback。
func NewPrometheusCallback(exporter *MetricsExporter) *PrometheusCallback {
	return &PrometheusCallback{
		exporter: exporter,
	}
}

// OnAgentStart Agent 启动时记录指标。
func (c *PrometheusCallback) OnAgentStart(ctx context.Context, event AgentStartEvent) {
	c.exporter.agentStartTotal.WithLabelValues(event.AgentName).Inc()
	c.exporter.activeAgents.Inc()
}

// OnAgentEnd Agent 结束时记录指标。
func (c *PrometheusCallback) OnAgentEnd(ctx context.Context, event AgentEndEvent) {
	status := "success"
	if event.Error != nil {
		status = "error"
		c.exporter.agentErrorTotal.WithLabelValues(event.AgentName).Inc()
	}

	c.exporter.agentEndTotal.WithLabelValues(event.AgentName, status).Inc()
	c.exporter.agentDuration.WithLabelValues(event.AgentName).Observe(event.Duration.Seconds())
	c.exporter.activeAgents.Dec()
}

// OnToolStart Tool 执行前记录。
func (c *PrometheusCallback) OnToolStart(ctx context.Context, event ToolStartEvent) {
	c.exporter.toolCallTotal.WithLabelValues(event.ToolName).Inc()
}

// OnToolEnd Tool 执行后记录指标。
func (c *PrometheusCallback) OnToolEnd(ctx context.Context, event ToolEndEvent) {
	if event.Error != nil {
		c.exporter.toolErrorTotal.WithLabelValues(event.ToolName).Inc()
	}
	c.exporter.toolDuration.WithLabelValues(event.ToolName).Observe(event.Duration.Seconds())
}

// OnLLMStart LLM 调用前记录。
func (c *PrometheusCallback) OnLLMStart(ctx context.Context, event LLMStartEvent) {
	c.exporter.llmCallTotal.WithLabelValues(event.ProviderID, event.ModelID).Inc()
}

// OnLLMEnd LLM 调用后记录指标。
func (c *PrometheusCallback) OnLLMEnd(ctx context.Context, event LLMEndEvent) {
	if event.Error != nil {
		c.exporter.llmErrorTotal.WithLabelValues(event.ProviderID, event.ModelID).Inc()
	} else {
		// 记录 Token 使用
		c.exporter.llmTokensTotal.WithLabelValues(
			event.ProviderID,
			event.ModelID,
			"input",
		).Add(float64(event.Response.Usage.InTokens))

		c.exporter.llmTokensTotal.WithLabelValues(
			event.ProviderID,
			event.ModelID,
			"output",
		).Add(float64(event.Response.Usage.OutTokens))

		if event.Response.Usage.CachedTokens > 0 {
			c.exporter.llmCachedTokens.WithLabelValues(
				event.ProviderID,
				event.ModelID,
			).Add(float64(event.Response.Usage.CachedTokens))
		}
	}

	c.exporter.llmDuration.WithLabelValues(event.ProviderID, event.ModelID).Observe(event.Duration.Seconds())
}

// ─────────────────────────────────────────────
//  系统指标更新
// ─────────────────────────────────────────────

// SetActiveAgents 设置当前活跃 Agent 数量。
func (e *MetricsExporter) SetActiveAgents(count float64) {
	e.activeAgents.Set(count)
}

// SetQueuedTasks 设置队列中等待的任务数量。
func (e *MetricsExporter) SetQueuedTasks(count float64) {
	e.queuedTasks.Set(count)
}

// ─────────────────────────────────────────────
//  指标服务器
// ─────────────────────────────────────────────

// MetricsServer 是 Prometheus 指标服务器。
type MetricsServer struct {
	exporter *MetricsExporter
	server   *http.Server
	mu       sync.Mutex
}

// NewMetricsServer 创建指标服务器。
func NewMetricsServer(exporter *MetricsExporter, addr string) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", exporter.Handler())

	return &MetricsServer{
		exporter: exporter,
		server: &http.Server{
			Addr:              addr,
			Handler:           mux,
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       10 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
		},
	}
}

// Start 启动指标服务器。
func (s *MetricsServer) Start() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	go func() {
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Printf("指标服务器错误: %v\n", err)
		}
	}()

	return nil
}

// Shutdown 优雅关闭指标服务器。
func (s *MetricsServer) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.server.Shutdown(ctx)
}
