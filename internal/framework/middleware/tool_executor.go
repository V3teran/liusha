package middleware

import (
	"context"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
)

// ToolExecutorImpl 是工具执行器的实现（带中间件链）。
type ToolExecutorImpl struct {
	middlewares []core.ToolMiddleware
}

// NewToolExecutor 创建工具执行器。
func NewToolExecutor() *ToolExecutorImpl {
	return &ToolExecutorImpl{
		middlewares: make([]core.ToolMiddleware, 0),
	}
}

// Use 添加中间件。
func (e *ToolExecutorImpl) Use(middleware core.ToolMiddleware) {
	e.middlewares = append(e.middlewares, middleware)
}

// Execute 执行工具（带中间件链）。
func (e *ToolExecutorImpl) Execute(ctx context.Context, tool core.Tool, input core.ToolInput) (core.ToolOutput, error) {
	// Before 中间件
	for _, mw := range e.middlewares {
		var err error
		ctx, input, err = mw.Before(ctx, tool, input)
		if err != nil {
			return core.ToolOutput{Error: err.Error()}, err
		}
	}

	// 执行工具
	output, err := tool.Execute(ctx, input)

	// OnError 中间件
	if err != nil {
		for _, mw := range e.middlewares {
			if mwErr := mw.OnError(ctx, tool, err); mwErr != nil {
				return output, mwErr
			}
		}
		return output, err
	}

	// After 中间件（倒序执行）
	for i := len(e.middlewares) - 1; i >= 0; i-- {
		var err error
		output, err = e.middlewares[i].After(ctx, tool, output)
		if err != nil {
			return output, err
		}
	}

	return output, nil
}

// ============================================
// 内置中间件
// ============================================

// LoggingMiddleware 日志记录中间件。
type LoggingMiddleware struct {
	logger interface {
		Info(msg string, fields ...interface{})
		Error(msg string, fields ...interface{})
	}
}

// NewLoggingMiddleware 创建日志中间件。
func NewLoggingMiddleware(logger interface {
	Info(msg string, fields ...interface{})
	Error(msg string, fields ...interface{})
}) *LoggingMiddleware {
	return &LoggingMiddleware{logger: logger}
}

// Before 实现 ToolMiddleware 接口。
func (m *LoggingMiddleware) Before(ctx context.Context, tool core.Tool, input core.ToolInput) (context.Context, core.ToolInput, error) {
	m.logger.Info("tool execution started", "tool", tool.Name(), "caller", input.Caller)
	return ctx, input, nil
}

// After 实现 ToolMiddleware 接口。
func (m *LoggingMiddleware) After(ctx context.Context, tool core.Tool, output core.ToolOutput) (core.ToolOutput, error) {
	if output.Error != "" {
		m.logger.Error("tool execution failed", "tool", tool.Name(), "error", output.Error)
	} else {
		m.logger.Info("tool execution completed", "tool", tool.Name())
	}
	return output, nil
}

// OnError 实现 ToolMiddleware 接口。
func (m *LoggingMiddleware) OnError(ctx context.Context, tool core.Tool, err error) error {
	m.logger.Error("tool execution error", "tool", tool.Name(), "error", err.Error())
	return nil
}

// TimeoutMiddleware 超时控制中间件。
type TimeoutMiddleware struct {
	timeout time.Duration
}

// NewTimeoutMiddleware 创建超时中间件。
func NewTimeoutMiddleware(timeout time.Duration) *TimeoutMiddleware {
	return &TimeoutMiddleware{timeout: timeout}
}

// Before 实现 ToolMiddleware 接口。
func (m *TimeoutMiddleware) Before(ctx context.Context, tool core.Tool, input core.ToolInput) (context.Context, core.ToolInput, error) {
	// 设置超时
	ctx, _ = context.WithTimeout(ctx, m.timeout)
	return ctx, input, nil
}

// After 实现 ToolMiddleware 接口。
func (m *TimeoutMiddleware) After(ctx context.Context, tool core.Tool, output core.ToolOutput) (core.ToolOutput, error) {
	return output, nil
}

// OnError 实现 ToolMiddleware 接口。
func (m *TimeoutMiddleware) OnError(ctx context.Context, tool core.Tool, err error) error {
	return nil
}

// MetricsMiddleware 指标收集中间件。
type MetricsMiddleware struct {
	metrics interface {
		RecordDuration(tool string, duration time.Duration)
		RecordSuccess(tool string)
		RecordFailure(tool string)
	}
}

// NewMetricsMiddleware 创建指标中间件。
func NewMetricsMiddleware(metrics interface {
	RecordDuration(tool string, duration time.Duration)
	RecordSuccess(tool string)
	RecordFailure(tool string)
}) *MetricsMiddleware {
	return &MetricsMiddleware{metrics: metrics}
}

// Before 实现 ToolMiddleware 接口。
func (m *MetricsMiddleware) Before(ctx context.Context, tool core.Tool, input core.ToolInput) (context.Context, core.ToolInput, error) {
	// 记录开始时间
	ctx = context.WithValue(ctx, "start_time", time.Now())
	return ctx, input, nil
}

// After 实现 ToolMiddleware 接口。
func (m *MetricsMiddleware) After(ctx context.Context, tool core.Tool, output core.ToolOutput) (core.ToolOutput, error) {
	// 计算耗时
	if startTime, ok := ctx.Value("start_time").(time.Time); ok {
		duration := time.Since(startTime)
		m.metrics.RecordDuration(tool.Name(), duration)
	}

	// 记录成功/失败
	if output.Error == "" {
		m.metrics.RecordSuccess(tool.Name())
	} else {
		m.metrics.RecordFailure(tool.Name())
	}

	return output, nil
}

// OnError 实现 ToolMiddleware 接口。
func (m *MetricsMiddleware) OnError(ctx context.Context, tool core.Tool, err error) error {
	m.metrics.RecordFailure(tool.Name())
	return nil
}

// RetryMiddleware 重试中间件。
type RetryMiddleware struct {
	maxRetries int
	backoff    time.Duration
}

// NewRetryMiddleware 创建重试中间件。
func NewRetryMiddleware(maxRetries int, backoff time.Duration) *RetryMiddleware {
	return &RetryMiddleware{
		maxRetries: maxRetries,
		backoff:    backoff,
	}
}

// Before 实现 ToolMiddleware 接口。
func (m *RetryMiddleware) Before(ctx context.Context, tool core.Tool, input core.ToolInput) (context.Context, core.ToolInput, error) {
	return ctx, input, nil
}

// After 实现 ToolMiddleware 接口。
func (m *RetryMiddleware) After(ctx context.Context, tool core.Tool, output core.ToolOutput) (core.ToolOutput, error) {
	return output, nil
}

// OnError 实现 ToolMiddleware 接口（重试逻辑需要在执行器层实现）。
func (m *RetryMiddleware) OnError(ctx context.Context, tool core.Tool, err error) error {
	return nil
}

// RateLimitMiddleware 限流中间件。
type RateLimitMiddleware struct {
	limiter interface {
		Allow(tool string) bool
		Wait(tool string) error
	}
}

// NewRateLimitMiddleware 创建限流中间件。
func NewRateLimitMiddleware(limiter interface {
	Allow(tool string) bool
	Wait(tool string) error
}) *RateLimitMiddleware {
	return &RateLimitMiddleware{limiter: limiter}
}

// Before 实现 ToolMiddleware 接口。
func (m *RateLimitMiddleware) Before(ctx context.Context, tool core.Tool, input core.ToolInput) (context.Context, core.ToolInput, error) {
	// 检查是否允许执行
	if !m.limiter.Allow(tool.Name()) {
		// 等待
		if err := m.limiter.Wait(tool.Name()); err != nil {
			return ctx, input, err
		}
	}
	return ctx, input, nil
}

// After 实现 ToolMiddleware 接口。
func (m *RateLimitMiddleware) After(ctx context.Context, tool core.Tool, output core.ToolOutput) (core.ToolOutput, error) {
	return output, nil
}

// OnError 实现 ToolMiddleware 接口。
func (m *RateLimitMiddleware) OnError(ctx context.Context, tool core.Tool, err error) error {
	return nil
}

// ValidationMiddleware 输入输出验证中间件。
type ValidationMiddleware struct {
	validator interface {
		ValidateInput(tool core.Tool, input core.ToolInput) error
		ValidateOutput(tool core.Tool, output core.ToolOutput) error
	}
}

// NewValidationMiddleware 创建验证中间件。
func NewValidationMiddleware(validator interface {
	ValidateInput(tool core.Tool, input core.ToolInput) error
	ValidateOutput(tool core.Tool, output core.ToolOutput) error
}) *ValidationMiddleware {
	return &ValidationMiddleware{validator: validator}
}

// Before 实现 ToolMiddleware 接口。
func (m *ValidationMiddleware) Before(ctx context.Context, tool core.Tool, input core.ToolInput) (context.Context, core.ToolInput, error) {
	if err := m.validator.ValidateInput(tool, input); err != nil {
		return ctx, input, err
	}
	return ctx, input, nil
}

// After 实现 ToolMiddleware 接口。
func (m *ValidationMiddleware) After(ctx context.Context, tool core.Tool, output core.ToolOutput) (core.ToolOutput, error) {
	if err := m.validator.ValidateOutput(tool, output); err != nil {
		return output, err
	}
	return output, nil
}

// OnError 实现 ToolMiddleware 接口。
func (m *ValidationMiddleware) OnError(ctx context.Context, tool core.Tool, err error) error {
	return nil
}
