package core

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/llm"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

// ─────────────────────────────────────────────
//  Tracer 测试
// ─────────────────────────────────────────────

func TestOtelTracer_Basic(t *testing.T) {
	// 配置 TracerProvider
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	tracer := NewOtelTracer("test-service")
	ctx := context.Background()

	// 创建 span
	ctx, span := tracer.StartSpan(ctx, "test-operation",
		WithSpanKind(SpanKindServer),
		WithAttributes(
			NewAttr("test.key", "test.value"),
			NewAttr("test.count", 42),
		),
	)
	defer span.End()

	// 验证 span 不为 nil
	if span == nil {
		t.Fatal("span 不应为 nil")
	}

	// 设置属性
	span.SetAttributes(
		NewAttr("operation.result", "success"),
		NewAttr("operation.duration", 123.45),
	)

	// 设置状态
	span.SetStatus(StatusCodeOK, "操作成功")

	// 添加事件
	span.AddEvent("checkpoint-1", NewAttr("step", 1))
	span.AddEvent("checkpoint-2", NewAttr("step", 2))

	// 获取 span context
	sc := span.SpanContext()
	if sc.TraceID == "" {
		t.Error("TraceID 不应为空")
	}
	if sc.SpanID == "" {
		t.Error("SpanID 不应为空")
	}
}

func TestOtelTracer_NestedSpans(t *testing.T) {
	// 配置 TracerProvider
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	tracer := NewOtelTracer("test-service")
	ctx := context.Background()

	// 父 span
	ctx, parentSpan := tracer.StartSpan(ctx, "parent-operation")
	defer parentSpan.End()

	parentSC := parentSpan.SpanContext()

	// 子 span
	ctx, childSpan := tracer.StartSpan(ctx, "child-operation")
	defer childSpan.End()

	childSC := childSpan.SpanContext()

	// 验证 TraceID 相同（同一追踪链路）
	if parentSC.TraceID != childSC.TraceID {
		t.Errorf("父子 span 的 TraceID 应相同，父=%s, 子=%s", parentSC.TraceID, childSC.TraceID)
	}

	// 验证 SpanID 不同
	if parentSC.SpanID == childSC.SpanID {
		t.Error("父子 span 的 SpanID 应不同")
	}
}

func TestOtelTracer_ErrorRecording(t *testing.T) {
	// 配置 TracerProvider
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	tracer := NewOtelTracer("test-service")
	ctx := context.Background()

	ctx, span := tracer.StartSpan(ctx, "error-operation")
	defer span.End()

	// 记录错误
	err := errors.New("测试错误")
	span.RecordError(err)
	span.SetStatus(StatusCodeError, err.Error())

	// 验证可以正常结束
	span.End()
}

func TestOtelTracer_Extract(t *testing.T) {
	// 配置 TracerProvider
	tp := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	defer tp.Shutdown(context.Background())

	tracer := NewOtelTracer("test-service")
	ctx := context.Background()

	// 创建 span 并注入上下文
	ctx, span := tracer.StartSpan(ctx, "test-operation")
	defer span.End()

	// 从上下文中提取
	extractedSpan := tracer.Extract(ctx)
	if extractedSpan == nil {
		t.Fatal("提取的 span 不应为 nil")
	}

	// 验证是同一个 span
	originalSC := span.SpanContext()
	extractedSC := extractedSpan.SpanContext()
	if originalSC.SpanID != extractedSC.SpanID {
		t.Error("提取的 span 应与原始 span 相同")
	}
}

// ─────────────────────────────────────────────
//  NoopTracer 测试
// ─────────────────────────────────────────────

func TestNoopTracer_Basic(t *testing.T) {
	tracer := NewNoopTracer()
	ctx := context.Background()

	// Noop tracer 应该正常工作但不执行任何操作
	ctx, span := tracer.StartSpan(ctx, "noop-operation")
	if span == nil {
		t.Fatal("noop span 不应为 nil")
	}

	span.SetAttributes(NewAttr("key", "value"))
	span.SetStatus(StatusCodeOK, "ok")
	span.RecordError(errors.New("error"))
	span.AddEvent("event")
	sc := span.SpanContext()

	// Noop span context 应该为空
	if sc.TraceID != "" || sc.SpanID != "" {
		t.Error("noop span context 应为空")
	}

	span.End()
}

// ─────────────────────────────────────────────
//  TracingCallback 测试
// ─────────────────────────────────────────────

func TestTracingCallback_AgentLifecycle(t *testing.T) {
	tracer := NewOtelTracer("test-service")
	callback := NewTracingCallback(tracer)
	ctx := context.Background()

	// Agent 启动
	startEvent := AgentStartEvent{
		TaskID:    "task-123",
		AgentName: "test-agent",
		StartTime: time.Now(),
	}
	callback.OnAgentStart(ctx, startEvent)

	// Agent 结束（成功）
	endEvent := AgentEndEvent{
		TaskID:    "task-123",
		AgentName: "test-agent",
		Duration:  100 * time.Millisecond,
		Error:     nil,
		StartTime: time.Now(),
	}
	callback.OnAgentEnd(ctx, endEvent)

	// 不应 panic
}

func TestTracingCallback_AgentLifecycle_WithError(t *testing.T) {
	tracer := NewOtelTracer("test-service")
	callback := NewTracingCallback(tracer)
	ctx := context.Background()

	// Agent 启动
	startEvent := AgentStartEvent{
		TaskID:    "task-456",
		AgentName: "test-agent",
		StartTime: time.Now(),
	}
	callback.OnAgentStart(ctx, startEvent)

	// Agent 结束（失败）
	endEvent := AgentEndEvent{
		TaskID:    "task-456",
		AgentName: "test-agent",
		Duration:  50 * time.Millisecond,
		Error:     errors.New("执行失败"),
		StartTime: time.Now(),
	}
	callback.OnAgentEnd(ctx, endEvent)

	// 不应 panic
}

func TestTracingCallback_ToolLifecycle(t *testing.T) {
	tracer := NewOtelTracer("test-service")
	callback := NewTracingCallback(tracer)
	ctx := context.Background()

	// Tool 启动
	startEvent := ToolStartEvent{
		TaskID:    "task-123",
		ToolName:  "test-tool",
		Input:     map[string]any{"key": "value"},
		StartTime: time.Now(),
	}
	callback.OnToolStart(ctx, startEvent)

	// Tool 结束
	endEvent := ToolEndEvent{
		TaskID:    "task-123",
		ToolName:  "test-tool",
		Output:    "result",
		Duration:  10 * time.Millisecond,
		Error:     nil,
		StartTime: time.Now(),
	}
	callback.OnToolEnd(ctx, endEvent)
}

func TestTracingCallback_LLMLifecycle(t *testing.T) {
	tracer := NewOtelTracer("test-service")
	callback := NewTracingCallback(tracer)
	ctx := context.Background()

	// LLM 启动
	startEvent := LLMStartEvent{
		TaskID:     "task-123",
		ProviderID: "openai",
		ModelID:    "gpt-4",
		Request:    llm.Request{},
		StartTime:  time.Now(),
	}
	callback.OnLLMStart(ctx, startEvent)

	// LLM 结束
	endEvent := LLMEndEvent{
		TaskID:     "task-123",
		ProviderID: "openai",
		ModelID:    "gpt-4",
		Response:   llm.Response{},
		Duration:   200 * time.Millisecond,
		Error:      nil,
		StartTime:  time.Now(),
	}
	callback.OnLLMEnd(ctx, endEvent)
}

func TestTracingCallback_Concurrent(t *testing.T) {
	tracer := NewOtelTracer("test-service")
	callback := NewTracingCallback(tracer)

	const numGoroutines = 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer func() { done <- true }()

			ctx := context.Background()
			taskID := "task-" + string(rune('0'+id))

			// Agent 生命周期
			callback.OnAgentStart(ctx, AgentStartEvent{
				TaskID:    taskID,
				AgentName: "agent-" + string(rune('0'+id)),
				StartTime: time.Now(),
			})

			time.Sleep(10 * time.Millisecond)

			callback.OnAgentEnd(ctx, AgentEndEvent{
				TaskID:    taskID,
				AgentName: "agent-" + string(rune('0'+id)),
				Duration:  10 * time.Millisecond,
				Error:     nil,
				StartTime: time.Now(),
			})
		}(i)
	}

	// 等待所有 goroutine 完成
	for i := 0; i < numGoroutines; i++ {
		<-done
	}
}

// ─────────────────────────────────────────────
//  辅助函数测试
// ─────────────────────────────────────────────

func TestNewAttr(t *testing.T) {
	tests := []struct {
		name  string
		key   string
		value any
	}{
		{"string", "key1", "value1"},
		{"int", "key2", 42},
		{"int64", "key3", int64(12345)},
		{"float64", "key4", 3.14},
		{"bool", "key5", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			attr := NewAttr(tt.key, tt.value)
			if attr.Key != tt.key {
				t.Errorf("key = %v, want %v", attr.Key, tt.key)
			}
			if attr.Value != tt.value {
				t.Errorf("value = %v, want %v", attr.Value, tt.value)
			}
		})
	}
}

func TestSpanFromContext(t *testing.T) {
	tracer := NewOtelTracer("test-service")
	ctx := context.Background()

	// 创建 span
	ctx, span := tracer.StartSpan(ctx, "test-operation")
	defer span.End()

	// 从上下文提取
	extractedSpan := SpanFromContext(ctx, tracer)
	if extractedSpan == nil {
		t.Fatal("提取的 span 不应为 nil")
	}

	// 验证 SpanID 相同
	originalSC := span.SpanContext()
	extractedSC := extractedSpan.SpanContext()
	if originalSC.SpanID != extractedSC.SpanID {
		t.Error("提取的 span 应与原始 span 相同")
	}
}

func TestContextWithSpan(t *testing.T) {
	tracer := NewOtelTracer("test-service")
	ctx := context.Background()

	// 创建 span
	_, span := tracer.StartSpan(ctx, "test-operation")
	defer span.End()

	// 注入到新上下文
	newCtx := ContextWithSpan(ctx, tracer, span)

	// 从新上下文提取
	extractedSpan := SpanFromContext(newCtx, tracer)
	if extractedSpan == nil {
		t.Fatal("提取的 span 不应为 nil")
	}

	// 验证 SpanID 相同
	originalSC := span.SpanContext()
	extractedSC := extractedSpan.SpanContext()
	if originalSC.SpanID != extractedSC.SpanID {
		t.Error("注入和提取的 span 应相同")
	}
}
