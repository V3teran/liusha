package core

import (
	"context"
	"testing"
	"time"
)

// ─────────────────────────────────────────────
//  ObservabilityConfig 测试
// ─────────────────────────────────────────────

func TestDefaultObservabilityConfig(t *testing.T) {
	config := DefaultObservabilityConfig()

	if config.ServiceName != "liusha-adk" {
		t.Errorf("ServiceName = %s, want liusha-adk", config.ServiceName)
	}

	if !config.Logging.Enabled {
		t.Error("Logging 应该默认启用")
	}

	if !config.Metrics.Enabled {
		t.Error("Metrics 应该默认启用")
	}

	if !config.Tracing.Enabled {
		t.Error("Tracing 应该默认启用")
	}

	if !config.Profiling.Enabled {
		t.Error("Profiling 应该默认启用")
	}

	// Prometheus 默认不启用
	if config.Metrics.Prometheus.Enabled {
		t.Error("Prometheus 应该默认不启用")
	}

	// Tracing 默认使用 noop
	if config.Tracing.Exporter != "noop" {
		t.Errorf("Tracing.Exporter = %s, want noop", config.Tracing.Exporter)
	}
}

// ─────────────────────────────────────────────
//  ObservabilityManager 测试
// ─────────────────────────────────────────────

func TestObservabilityManager_Creation(t *testing.T) {
	config := DefaultObservabilityConfig()
	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	if om == nil {
		t.Fatal("ObservabilityManager 不应为 nil")
	}

	if om.logger == nil {
		t.Error("logger 应该被创建")
	}

	if om.metricsCallback == nil {
		t.Error("metricsCallback 应该被创建")
	}

	if om.tracer == nil {
		t.Error("tracer 应该被创建")
	}
}

func TestObservabilityManager_DisabledComponents(t *testing.T) {
	config := DefaultObservabilityConfig()
	config.Logging.Enabled = false
	config.Metrics.Enabled = false
	config.Tracing.Enabled = false
	config.Profiling.Enabled = false

	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	if om.logger != nil {
		t.Error("logger 应该为 nil（已禁用）")
	}

	if om.metricsCallback != nil {
		t.Error("metricsCallback 应该为 nil（已禁用）")
	}

	// tracer 即使禁用也应该有 noop 实现
	if om.tracer == nil {
		t.Error("tracer 不应为 nil（应该是 noop）")
	}
}

func TestObservabilityManager_GetCallbacks(t *testing.T) {
	config := DefaultObservabilityConfig()
	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	callbacks := om.GetCallbacks()

	// 默认配置应该有 3 个 callbacks（logger, metrics, tracing）
	// Prometheus 默认不启用，所以不包含
	if len(callbacks) < 3 {
		t.Errorf("callbacks 数量 = %d, want >= 3", len(callbacks))
	}
}

func TestObservabilityManager_GetCallbacks_WithPrometheus(t *testing.T) {
	config := DefaultObservabilityConfig()
	config.Metrics.Prometheus.Enabled = true
	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	callbacks := om.GetCallbacks()

	// 启用 Prometheus 后应该有 4 个 callbacks
	if len(callbacks) < 4 {
		t.Errorf("callbacks 数量 = %d, want >= 4", len(callbacks))
	}
}

func TestObservabilityManager_GetMetrics(t *testing.T) {
	config := DefaultObservabilityConfig()
	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	metrics := om.GetMetrics()
	if metrics == nil {
		t.Fatal("metrics 不应为 nil")
	}

	// 初始指标应该为 0
	if metrics.AgentStartCount != 0 {
		t.Errorf("AgentStartCount = %d, want 0", metrics.AgentStartCount)
	}
}

func TestObservabilityManager_GetTracer(t *testing.T) {
	config := DefaultObservabilityConfig()
	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	tracer := om.GetTracer()
	if tracer == nil {
		t.Fatal("tracer 不应为 nil")
	}

	// 测试 tracer 基本功能
	ctx := context.Background()
	ctx, span := tracer.StartSpan(ctx, "test-span")
	defer span.End()

	if span == nil {
		t.Error("span 不应为 nil")
	}
}

func TestObservabilityManager_GetProfiler(t *testing.T) {
	config := DefaultObservabilityConfig()
	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	profiler := om.GetProfiler()
	if profiler == nil {
		t.Error("profiler 不应为 nil")
	}

	// 测试 profiler 基本功能
	stats := profiler.GetRuntimeStats()
	if stats.NumGoroutine <= 0 {
		t.Error("NumGoroutine 应该 > 0")
	}
}

func TestObservabilityManager_StartShutdown(t *testing.T) {
	config := DefaultObservabilityConfig()
	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	// 启动
	ctx := context.Background()
	if err := om.Start(ctx); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	// 等待一段时间
	time.Sleep(100 * time.Millisecond)

	// 关闭
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := om.Shutdown(shutdownCtx); err != nil {
		t.Errorf("关闭失败: %v", err)
	}
}

func TestObservabilityManager_WithPrometheus(t *testing.T) {
	config := DefaultObservabilityConfig()
	config.Metrics.Prometheus.Enabled = true
	config.Metrics.Prometheus.Addr = "localhost:0" // 随机端口

	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	if om.metricsExporter == nil {
		t.Error("metricsExporter 应该被创建")
	}

	if om.metricsServer == nil {
		t.Error("metricsServer 应该被创建")
	}

	// 启动
	ctx := context.Background()
	if err := om.Start(ctx); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// 关闭
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := om.Shutdown(shutdownCtx); err != nil {
		t.Errorf("关闭失败: %v", err)
	}
}

func TestObservabilityManager_TracingStdout(t *testing.T) {
	config := DefaultObservabilityConfig()
	config.Tracing.Exporter = "stdout"

	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建 ObservabilityManager 失败: %v", err)
	}

	if om.tracerProvider == nil {
		t.Error("tracerProvider 应该被创建")
	}

	// 测试追踪
	tracer := om.GetTracer()
	ctx := context.Background()
	ctx, span := tracer.StartSpan(ctx, "test-span")
	span.SetAttributes(NewAttr("test.key", "test.value"))
	span.End()

	// 关闭
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := om.Shutdown(shutdownCtx); err != nil {
		t.Errorf("关闭失败: %v", err)
	}
}

// ─────────────────────────────────────────────
//  便捷函数测试
// ─────────────────────────────────────────────

func TestNewDefaultObservability(t *testing.T) {
	om, err := NewDefaultObservability("test-service")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	if om == nil {
		t.Fatal("ObservabilityManager 不应为 nil")
	}

	if om.config.ServiceName != "test-service" {
		t.Errorf("ServiceName = %s, want test-service", om.config.ServiceName)
	}
}

func TestNewProductionObservability(t *testing.T) {
	om, err := NewProductionObservability("prod-service", ":9091")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	if om == nil {
		t.Fatal("ObservabilityManager 不应为 nil")
	}

	if om.config.ServiceName != "prod-service" {
		t.Errorf("ServiceName = %s, want prod-service", om.config.ServiceName)
	}

	if om.config.Logging.Level != "warn" {
		t.Errorf("Logging.Level = %s, want warn", om.config.Logging.Level)
	}

	if !om.config.Metrics.Prometheus.Enabled {
		t.Error("Prometheus 应该启用")
	}

	if om.config.Metrics.Prometheus.Addr != ":9091" {
		t.Errorf("Prometheus.Addr = %s, want :9091", om.config.Metrics.Prometheus.Addr)
	}

	if om.config.Tracing.SamplingRate != 0.1 {
		t.Errorf("Tracing.SamplingRate = %.2f, want 0.1", om.config.Tracing.SamplingRate)
	}
}

func TestNewDevelopmentObservability(t *testing.T) {
	om, err := NewDevelopmentObservability("dev-service")
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	if om == nil {
		t.Fatal("ObservabilityManager 不应为 nil")
	}

	if om.config.ServiceName != "dev-service" {
		t.Errorf("ServiceName = %s, want dev-service", om.config.ServiceName)
	}

	if om.config.Logging.Level != "debug" {
		t.Errorf("Logging.Level = %s, want debug", om.config.Logging.Level)
	}

	if om.config.Logging.Format != "console" {
		t.Errorf("Logging.Format = %s, want console", om.config.Logging.Format)
	}

	if om.config.Metrics.Prometheus.Enabled {
		t.Error("Prometheus 应该禁用")
	}

	if om.config.Tracing.Exporter != "stdout" {
		t.Errorf("Tracing.Exporter = %s, want stdout", om.config.Tracing.Exporter)
	}

	if om.config.Tracing.SamplingRate != 1.0 {
		t.Errorf("Tracing.SamplingRate = %.2f, want 1.0", om.config.Tracing.SamplingRate)
	}
}

// ─────────────────────────────────────────────
//  集成测试
// ─────────────────────────────────────────────

func TestObservabilityManager_EndToEnd(t *testing.T) {
	// 创建完整配置的 manager
	config := DefaultObservabilityConfig()
	config.ServiceName = "test-e2e"
	config.Tracing.Exporter = "stdout"

	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	// 启动
	ctx := context.Background()
	if err := om.Start(ctx); err != nil {
		t.Fatalf("启动失败: %v", err)
	}

	// 获取 callbacks
	callbacks := om.GetCallbacks()
	if len(callbacks) == 0 {
		t.Fatal("callbacks 不应为空")
	}

	// 模拟 Agent 生命周期
	for _, cb := range callbacks {
		cb.OnAgentStart(ctx, AgentStartEvent{
			TaskID:    "task-e2e",
			AgentName: "test-agent",
			StartTime: time.Now(),
		})
	}

	time.Sleep(50 * time.Millisecond)

	for _, cb := range callbacks {
		cb.OnAgentEnd(ctx, AgentEndEvent{
			TaskID:    "task-e2e",
			AgentName: "test-agent",
			Duration:  50 * time.Millisecond,
			Error:     nil,
			StartTime: time.Now(),
		})
	}

	// 验证指标
	metrics := om.GetMetrics()
	if metrics.AgentStartCount == 0 {
		t.Error("AgentStartCount 应该 > 0")
	}

	if metrics.AgentEndCount == 0 {
		t.Error("AgentEndCount 应该 > 0")
	}

	// 关闭
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := om.Shutdown(shutdownCtx); err != nil {
		t.Errorf("关闭失败: %v", err)
	}
}

func TestObservabilityManager_MultipleStarts(t *testing.T) {
	config := DefaultObservabilityConfig()
	om, err := NewObservabilityManager(config)
	if err != nil {
		t.Fatalf("创建失败: %v", err)
	}

	ctx := context.Background()

	// 第一次启动
	if err := om.Start(ctx); err != nil {
		t.Fatalf("第一次启动失败: %v", err)
	}

	// 第二次启动（应该正常，不报错）
	if err := om.Start(ctx); err != nil {
		t.Errorf("第二次启动失败: %v", err)
	}

	// 关闭
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := om.Shutdown(shutdownCtx); err != nil {
		t.Errorf("关闭失败: %v", err)
	}
}
