package core

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/exporters/stdout/stdouttrace"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
)

// ObservabilityConfig 是可观测性统一配置。
type ObservabilityConfig struct {
	// ServiceName 服务名称
	ServiceName string

	// Logging 日志配置
	Logging LoggingConfig

	// Metrics 指标配置
	Metrics MetricsConfig

	// Tracing 追踪配置
	Tracing TracingConfig

	// Profiling 性能剖析配置
	Profiling ProfilerConfig
}

// LoggingConfig 是日志配置。
type LoggingConfig struct {
	// Enabled 是否启用日志
	Enabled bool

	// Level 日志级别：debug, info, warn, error
	Level string

	// Format 日志格式：json, console
	Format string

	// Output 输出目标：stdout, stderr, file
	Output string

	// FilePath 日志文件路径（当 Output=file 时）
	FilePath string
}

// MetricsConfig 是指标配置。
type MetricsConfig struct {
	// Enabled 是否启用指标收集
	Enabled bool

	// Prometheus Prometheus 导出配置
	Prometheus PrometheusConfig
}

// PrometheusConfig 是 Prometheus 配置。
type PrometheusConfig struct {
	// Enabled 是否启用 Prometheus 导出
	Enabled bool

	// Addr 监听地址，如 ":9090"
	Addr string

	// Path 指标路径，默认 "/metrics"
	Path string

	// Namespace 指标命名空间
	Namespace string
}

// TracingConfig 是追踪配置。
type TracingConfig struct {
	// Enabled 是否启用追踪
	Enabled bool

	// Exporter 导出器类型：noop, stdout, otlp, jaeger
	Exporter string

	// SamplingRate 采样率，0.0-1.0
	SamplingRate float64

	// OTLP OTLP 导出配置
	OTLP OTLPConfig

	// Jaeger Jaeger 导出配置
	Jaeger JaegerConfig
}

// OTLPConfig 是 OTLP 导出配置。
type OTLPConfig struct {
	// Endpoint OTLP 端点，如 "localhost:4317"
	Endpoint string

	// Insecure 是否使用非加密连接
	Insecure bool

	// Headers 额外的 HTTP 头
	Headers map[string]string
}

// JaegerConfig 是 Jaeger 导出配置。
type JaegerConfig struct {
	// Endpoint Jaeger 端点，如 "http://localhost:14268/api/traces"
	Endpoint string

	// AgentEndpoint Jaeger Agent 端点，如 "localhost:6831"
	AgentEndpoint string
}

// DefaultObservabilityConfig 返回默认配置。
func DefaultObservabilityConfig() ObservabilityConfig {
	return ObservabilityConfig{
		ServiceName: "liusha-adk",
		Logging: LoggingConfig{
			Enabled: true,
			Level:   "info",
			Format:  "json",
			Output:  "stdout",
		},
		Metrics: MetricsConfig{
			Enabled: true,
			Prometheus: PrometheusConfig{
				Enabled:   false, // 默认不启用，用户可选
				Addr:      ":9090",
				Path:      "/metrics",
				Namespace: "liusha_adk",
			},
		},
		Tracing: TracingConfig{
			Enabled:      true,
			Exporter:     "noop", // 默认不导出
			SamplingRate: 1.0,    // 全采样
		},
		Profiling: DefaultProfilerConfig(),
	}
}

// ─────────────────────────────────────────────
//  可观测性管理器
// ─────────────────────────────────────────────

// ObservabilityManager 是可观测性统一管理器。
type ObservabilityManager struct {
	config ObservabilityConfig
	mu     sync.RWMutex

	// 组件
	logger          *LoggerCallback
	metricsCallback *MetricsCallback
	metricsExporter *MetricsExporter
	metricsServer   *MetricsServer
	tracer          Tracer
	tracingCallback *TracingCallback
	profiler        *Profiler

	// 追踪相关
	tracerProvider *sdktrace.TracerProvider
}

// NewObservabilityManager 创建可观测性管理器。
func NewObservabilityManager(config ObservabilityConfig) (*ObservabilityManager, error) {
	om := &ObservabilityManager{
		config: config,
	}

	// 初始化日志
	if config.Logging.Enabled {
		// 创建 zerolog logger
		var level zerolog.Level
		switch config.Logging.Level {
		case "debug":
			level = zerolog.DebugLevel
		case "info":
			level = zerolog.InfoLevel
		case "warn":
			level = zerolog.WarnLevel
		case "error":
			level = zerolog.ErrorLevel
		default:
			level = zerolog.InfoLevel
		}

		logger := zerolog.New(os.Stdout).Level(level).With().Timestamp().Logger()
		om.logger = NewLoggerCallback(logger)
	}

	// 初始化指标
	if config.Metrics.Enabled {
		om.metricsCallback = NewMetricsCallback()

		// Prometheus 导出
		if config.Metrics.Prometheus.Enabled {
			om.metricsExporter = NewMetricsExporter(config.Metrics.Prometheus.Namespace)
			om.metricsServer = NewMetricsServer(
				om.metricsExporter,
				config.Metrics.Prometheus.Addr,
			)
		}
	}

	// 初始化追踪
	if config.Tracing.Enabled {
		if err := om.initTracing(); err != nil {
			return nil, fmt.Errorf("初始化追踪失败: %w", err)
		}
	} else {
		om.tracer = NewNoopTracer()
	}

	om.tracingCallback = NewTracingCallback(om.tracer)

	// 初始化性能剖析
	if config.Profiling.Enabled {
		om.profiler = NewProfiler(config.Profiling)
	}

	return om, nil
}

// initTracing 初始化追踪。
func (om *ObservabilityManager) initTracing() error {
	var exporter sdktrace.SpanExporter
	var err error

	switch om.config.Tracing.Exporter {
	case "stdout":
		exporter, err = om.createStdoutExporter()
	case "noop":
		// 使用 noop tracer
		om.tracer = NewNoopTracer()
		return nil
	default:
		// 默认使用 noop
		om.tracer = NewNoopTracer()
		return nil
	}

	if err != nil {
		return fmt.Errorf("创建 exporter 失败: %w", err)
	}

	// 创建 resource
	res, err := resource.New(
		context.Background(),
		resource.WithAttributes(
			semconv.ServiceName(om.config.ServiceName),
		),
	)
	if err != nil {
		return fmt.Errorf("创建 resource 失败: %w", err)
	}

	// 创建 tracer provider
	om.tracerProvider = sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sdktrace.TraceIDRatioBased(om.config.Tracing.SamplingRate)),
	)

	om.tracer = NewOtelTracer(om.config.ServiceName)

	return nil
}

// createStdoutExporter 创建 stdout exporter。
func (om *ObservabilityManager) createStdoutExporter() (sdktrace.SpanExporter, error) {
	return stdouttrace.New(
		stdouttrace.WithPrettyPrint(),
	)
}

// Start 启动可观测性服务。
func (om *ObservabilityManager) Start(ctx context.Context) error {
	om.mu.Lock()
	defer om.mu.Unlock()

	// 启动 Prometheus 服务器
	if om.metricsServer != nil {
		if err := om.metricsServer.Start(); err != nil {
			return fmt.Errorf("启动指标服务器失败: %w", err)
		}
	}

	return nil
}

// Shutdown 关闭可观测性服务。
func (om *ObservabilityManager) Shutdown(ctx context.Context) error {
	om.mu.Lock()
	defer om.mu.Unlock()

	// 关闭 tracer provider
	if om.tracerProvider != nil {
		if err := om.tracerProvider.Shutdown(ctx); err != nil {
			return fmt.Errorf("关闭 tracer provider 失败: %w", err)
		}
	}

	// 关闭指标服务器
	if om.metricsServer != nil {
		if err := om.metricsServer.Shutdown(ctx); err != nil {
			return fmt.Errorf("关闭指标服务器失败: %w", err)
		}
	}

	return nil
}

// GetCallbacks 获取所有 Callback（用于注册到 Graph）。
func (om *ObservabilityManager) GetCallbacks() []Callback {
	callbacks := make([]Callback, 0, 4)

	if om.logger != nil {
		callbacks = append(callbacks, om.logger)
	}

	if om.metricsCallback != nil {
		callbacks = append(callbacks, om.metricsCallback)
	}

	if om.metricsExporter != nil {
		prometheusCallback := NewPrometheusCallback(om.metricsExporter)
		callbacks = append(callbacks, prometheusCallback)
	}

	if om.tracingCallback != nil {
		callbacks = append(callbacks, om.tracingCallback)
	}

	return callbacks
}

// GetMetrics 获取内存指标（从 MetricsCallback）。
func (om *ObservabilityManager) GetMetrics() *Metrics {
	if om.metricsCallback != nil {
		metrics := om.metricsCallback.GetMetrics()
		return &metrics
	}
	return nil
}

// GetTracer 获取 Tracer。
func (om *ObservabilityManager) GetTracer() Tracer {
	return om.tracer
}

// GetProfiler 获取 Profiler。
func (om *ObservabilityManager) GetProfiler() *Profiler {
	return om.profiler
}

// GetMetricsExporter 获取 Prometheus exporter。
func (om *ObservabilityManager) GetMetricsExporter() *MetricsExporter {
	return om.metricsExporter
}

// ─────────────────────────────────────────────
//  辅助类型：Stdout Trace Writer
// ─────────────────────────────────────────────

// StdoutTraceWriter 是标准输出追踪写入器。
type StdoutTraceWriter struct {
	writer io.Writer
	mu     sync.Mutex
}

// NewStdoutTraceWriter 创建标准输出追踪写入器。
func NewStdoutTraceWriter(w io.Writer) *StdoutTraceWriter {
	return &StdoutTraceWriter{
		writer: w,
	}
}

// Write 写入追踪数据。
func (w *StdoutTraceWriter) Write(p []byte) (n int, err error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.writer.Write(p)
}

// ─────────────────────────────────────────────
//  便捷函数
// ─────────────────────────────────────────────

// NewDefaultObservability 创建默认配置的可观测性管理器。
func NewDefaultObservability(serviceName string) (*ObservabilityManager, error) {
	config := DefaultObservabilityConfig()
	config.ServiceName = serviceName
	return NewObservabilityManager(config)
}

// NewProductionObservability 创建生产环境配置的可观测性管理器。
func NewProductionObservability(serviceName string, prometheusAddr string) (*ObservabilityManager, error) {
	config := DefaultObservabilityConfig()
	config.ServiceName = serviceName
	config.Logging.Level = "warn" // 生产环境减少日志
	config.Metrics.Prometheus.Enabled = true
	config.Metrics.Prometheus.Addr = prometheusAddr
	config.Tracing.Exporter = "stdout" // 生产环境可改为 otlp
	config.Tracing.SamplingRate = 0.1  // 10% 采样率
	config.Profiling.Enabled = true

	return NewObservabilityManager(config)
}

// NewDevelopmentObservability 创建开发环境配置的可观测性管理器。
func NewDevelopmentObservability(serviceName string) (*ObservabilityManager, error) {
	config := DefaultObservabilityConfig()
	config.ServiceName = serviceName
	config.Logging.Level = "debug"
	config.Logging.Format = "console"
	config.Metrics.Prometheus.Enabled = false // 开发环境不需要 Prometheus
	config.Tracing.Exporter = "stdout"        // 输出到控制台
	config.Tracing.SamplingRate = 1.0         // 全采样
	config.Profiling.Enabled = true

	return NewObservabilityManager(config)
}
