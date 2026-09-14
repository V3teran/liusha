package core

import (
	"context"
	"fmt"
	"sync"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// Tracer 是分布式追踪接口，基于 OpenTelemetry。
type Tracer interface {
	// StartSpan 开始一个新的 span
	StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span)

	// Extract 从上下文中提取 span
	Extract(ctx context.Context) Span

	// Inject 将 span 注入上下文
	Inject(ctx context.Context, span Span) context.Context
}

// Span 是追踪的基本单元。
type Span interface {
	// End 结束 span
	End()

	// SetAttributes 设置属性
	SetAttributes(attrs ...Attribute)

	// SetStatus 设置状态
	SetStatus(code StatusCode, description string)

	// RecordError 记录错误
	RecordError(err error)

	// AddEvent 添加事件
	AddEvent(name string, attrs ...Attribute)

	// SpanContext 获取 span 上下文
	SpanContext() SpanContext
}

// SpanContext 是 span 的上下文信息。
type SpanContext struct {
	TraceID string
	SpanID  string
}

// Attribute 是 span 的属性。
type Attribute struct {
	Key   string
	Value any
}

// StatusCode 是 span 的状态码。
type StatusCode int

const (
	StatusCodeUnset StatusCode = iota
	StatusCodeOK
	StatusCodeError
)

// SpanOption 是创建 span 的选项。
type SpanOption func(*spanConfig)

type spanConfig struct {
	spanKind   SpanKind
	attributes []Attribute
}

// SpanKind 是 span 的类型。
type SpanKind int

const (
	SpanKindInternal SpanKind = iota
	SpanKindServer
	SpanKindClient
	SpanKindProducer
	SpanKindConsumer
)

// WithSpanKind 设置 span 类型。
func WithSpanKind(kind SpanKind) SpanOption {
	return func(c *spanConfig) {
		c.spanKind = kind
	}
}

// WithAttributes 设置初始属性。
func WithAttributes(attrs ...Attribute) SpanOption {
	return func(c *spanConfig) {
		c.attributes = append(c.attributes, attrs...)
	}
}

// ─────────────────────────────────────────────
//  OpenTelemetry 实现
// ─────────────────────────────────────────────

// OtelTracer 是基于 OpenTelemetry 的 Tracer 实现。
type OtelTracer struct {
	tracer trace.Tracer
}

// NewOtelTracer 创建 OpenTelemetry Tracer。
func NewOtelTracer(serviceName string) *OtelTracer {
	return &OtelTracer{
		tracer: otel.Tracer(serviceName),
	}
}

// StartSpan 开始一个新的 span。
func (t *OtelTracer) StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span) {
	cfg := &spanConfig{}
	for _, opt := range opts {
		opt(cfg)
	}

	// 转换 span kind
	var otelKind trace.SpanKind
	switch cfg.spanKind {
	case SpanKindServer:
		otelKind = trace.SpanKindServer
	case SpanKindClient:
		otelKind = trace.SpanKindClient
	case SpanKindProducer:
		otelKind = trace.SpanKindProducer
	case SpanKindConsumer:
		otelKind = trace.SpanKindConsumer
	default:
		otelKind = trace.SpanKindInternal
	}

	// 转换属性
	otelAttrs := make([]attribute.KeyValue, len(cfg.attributes))
	for i, attr := range cfg.attributes {
		otelAttrs[i] = attributeToOtel(attr)
	}

	// 启动 span
	ctx, otelSpan := t.tracer.Start(ctx, name,
		trace.WithSpanKind(otelKind),
		trace.WithAttributes(otelAttrs...),
	)

	return ctx, &OtelSpan{span: otelSpan}
}

// Extract 从上下文中提取 span。
func (t *OtelTracer) Extract(ctx context.Context) Span {
	otelSpan := trace.SpanFromContext(ctx)
	if otelSpan == nil || !otelSpan.SpanContext().IsValid() {
		return &NoopSpan{}
	}
	return &OtelSpan{span: otelSpan}
}

// Inject 将 span 注入上下文。
func (t *OtelTracer) Inject(ctx context.Context, span Span) context.Context {
	if otelSpan, ok := span.(*OtelSpan); ok {
		return trace.ContextWithSpan(ctx, otelSpan.span)
	}
	return ctx
}

// OtelSpan 是 OpenTelemetry Span 的包装。
type OtelSpan struct {
	span trace.Span
}

// End 结束 span。
func (s *OtelSpan) End() {
	s.span.End()
}

// SetAttributes 设置属性。
func (s *OtelSpan) SetAttributes(attrs ...Attribute) {
	otelAttrs := make([]attribute.KeyValue, len(attrs))
	for i, attr := range attrs {
		otelAttrs[i] = attributeToOtel(attr)
	}
	s.span.SetAttributes(otelAttrs...)
}

// SetStatus 设置状态。
func (s *OtelSpan) SetStatus(code StatusCode, description string) {
	var otelCode codes.Code
	switch code {
	case StatusCodeOK:
		otelCode = codes.Ok
	case StatusCodeError:
		otelCode = codes.Error
	default:
		otelCode = codes.Unset
	}
	s.span.SetStatus(otelCode, description)
}

// RecordError 记录错误。
func (s *OtelSpan) RecordError(err error) {
	s.span.RecordError(err)
}

// AddEvent 添加事件。
func (s *OtelSpan) AddEvent(name string, attrs ...Attribute) {
	otelAttrs := make([]attribute.KeyValue, len(attrs))
	for i, attr := range attrs {
		otelAttrs[i] = attributeToOtel(attr)
	}
	s.span.AddEvent(name, trace.WithAttributes(otelAttrs...))
}

// SpanContext 获取 span 上下文。
func (s *OtelSpan) SpanContext() SpanContext {
	sc := s.span.SpanContext()
	return SpanContext{
		TraceID: sc.TraceID().String(),
		SpanID:  sc.SpanID().String(),
	}
}

// attributeToOtel 将 Attribute 转换为 OpenTelemetry 的 KeyValue。
func attributeToOtel(attr Attribute) attribute.KeyValue {
	switch v := attr.Value.(type) {
	case string:
		return attribute.String(attr.Key, v)
	case int:
		return attribute.Int(attr.Key, v)
	case int64:
		return attribute.Int64(attr.Key, v)
	case float64:
		return attribute.Float64(attr.Key, v)
	case bool:
		return attribute.Bool(attr.Key, v)
	default:
		return attribute.String(attr.Key, fmt.Sprintf("%v", v))
	}
}

// ─────────────────────────────────────────────
//  Noop 实现（用于禁用追踪）
// ─────────────────────────────────────────────

// NoopTracer 是不执行任何操作的 Tracer。
type NoopTracer struct{}

// NewNoopTracer 创建 Noop Tracer。
func NewNoopTracer() *NoopTracer {
	return &NoopTracer{}
}

// StartSpan 返回 noop span。
func (t *NoopTracer) StartSpan(ctx context.Context, name string, opts ...SpanOption) (context.Context, Span) {
	return ctx, &NoopSpan{}
}

// Extract 返回 noop span。
func (t *NoopTracer) Extract(ctx context.Context) Span {
	return &NoopSpan{}
}

// Inject 原样返回上下文。
func (t *NoopTracer) Inject(ctx context.Context, span Span) context.Context {
	return ctx
}

// NoopSpan 是不执行任何操作的 Span。
type NoopSpan struct{}

func (s *NoopSpan) End()                                           {}
func (s *NoopSpan) SetAttributes(attrs ...Attribute)               {}
func (s *NoopSpan) SetStatus(code StatusCode, description string)  {}
func (s *NoopSpan) RecordError(err error)                          {}
func (s *NoopSpan) AddEvent(name string, attrs ...Attribute)       {}
func (s *NoopSpan) SpanContext() SpanContext                       { return SpanContext{} }

// ─────────────────────────────────────────────
//  追踪回调（集成到 Callback 系统）
// ─────────────────────────────────────────────

// TracingCallback 是追踪 Callback 实现。
type TracingCallback struct {
	NoopCallback
	tracer Tracer
	spans  sync.Map // task_id -> Span
}

// NewTracingCallback 创建追踪 Callback。
func NewTracingCallback(tracer Tracer) *TracingCallback {
	return &TracingCallback{
		tracer: tracer,
	}
}

// OnAgentStart Agent 启动时创建 span。
func (t *TracingCallback) OnAgentStart(ctx context.Context, event AgentStartEvent) {
	ctx, span := t.tracer.StartSpan(ctx, "agent."+event.AgentName,
		WithSpanKind(SpanKindServer),
		WithAttributes(
			Attribute{Key: "agent.name", Value: event.AgentName},
			Attribute{Key: "task.id", Value: event.TaskID},
		),
	)
	t.spans.Store(event.TaskID, span)
}

// OnAgentEnd Agent 结束时完成 span。
func (t *TracingCallback) OnAgentEnd(ctx context.Context, event AgentEndEvent) {
	if spanVal, ok := t.spans.LoadAndDelete(event.TaskID); ok {
		span := spanVal.(Span)
		if event.Error != nil {
			span.SetStatus(StatusCodeError, event.Error.Error())
			span.RecordError(event.Error)
		} else {
			span.SetStatus(StatusCodeOK, "completed")
		}
		span.SetAttributes(
			Attribute{Key: "duration_ms", Value: event.Duration.Milliseconds()},
		)
		span.End()
	}
}

// OnToolStart Tool 执行前创建 span。
func (t *TracingCallback) OnToolStart(ctx context.Context, event ToolStartEvent) {
	_, span := t.tracer.StartSpan(ctx, "tool."+event.ToolName,
		WithSpanKind(SpanKindClient),
		WithAttributes(
			Attribute{Key: "tool.name", Value: event.ToolName},
			Attribute{Key: "task.id", Value: event.TaskID},
		),
	)
	t.spans.Store(event.TaskID+":tool:"+event.ToolName, span)
}

// OnToolEnd Tool 执行后完成 span。
func (t *TracingCallback) OnToolEnd(ctx context.Context, event ToolEndEvent) {
	key := event.TaskID + ":tool:" + event.ToolName
	if spanVal, ok := t.spans.LoadAndDelete(key); ok {
		span := spanVal.(Span)
		if event.Error != nil {
			span.SetStatus(StatusCodeError, event.Error.Error())
			span.RecordError(event.Error)
		} else {
			span.SetStatus(StatusCodeOK, "completed")
		}
		span.SetAttributes(
			Attribute{Key: "duration_ms", Value: event.Duration.Milliseconds()},
		)
		span.End()
	}
}

// OnLLMStart LLM 调用前创建 span。
func (t *TracingCallback) OnLLMStart(ctx context.Context, event LLMStartEvent) {
	_, span := t.tracer.StartSpan(ctx, "llm."+event.ModelID,
		WithSpanKind(SpanKindClient),
		WithAttributes(
			Attribute{Key: "llm.provider", Value: event.ProviderID},
			Attribute{Key: "llm.model", Value: event.ModelID},
			Attribute{Key: "task.id", Value: event.TaskID},
			Attribute{Key: "llm.messages_count", Value: len(event.Request.Messages)},
			Attribute{Key: "llm.tools_count", Value: len(event.Request.Tools)},
		),
	)
	t.spans.Store(event.TaskID+":llm", span)
}

// OnLLMEnd LLM 调用后完成 span。
func (t *TracingCallback) OnLLMEnd(ctx context.Context, event LLMEndEvent) {
	key := event.TaskID + ":llm"
	if spanVal, ok := t.spans.LoadAndDelete(key); ok {
		span := spanVal.(Span)
		if event.Error != nil {
			span.SetStatus(StatusCodeError, event.Error.Error())
			span.RecordError(event.Error)
		} else {
			span.SetStatus(StatusCodeOK, "completed")
			span.SetAttributes(
				Attribute{Key: "llm.in_tokens", Value: event.Response.Usage.InTokens},
				Attribute{Key: "llm.out_tokens", Value: event.Response.Usage.OutTokens},
				Attribute{Key: "llm.cached_tokens", Value: event.Response.Usage.CachedTokens},
				Attribute{Key: "llm.finish_reason", Value: event.Response.FinishReason},
			)
		}
		span.SetAttributes(
			Attribute{Key: "duration_ms", Value: event.Duration.Milliseconds()},
		)
		span.End()
	}
}

// ─────────────────────────────────────────────
//  辅助函数
// ─────────────────────────────────────────────

// NewAttr 创建属性的快捷函数。
func NewAttr(key string, value any) Attribute {
	return Attribute{Key: key, Value: value}
}

// SpanFromContext 从上下文中提取 span。
func SpanFromContext(ctx context.Context, tracer Tracer) Span {
	return tracer.Extract(ctx)
}

// ContextWithSpan 将 span 注入上下文。
func ContextWithSpan(ctx context.Context, tracer Tracer, span Span) context.Context {
	return tracer.Inject(ctx, span)
}
