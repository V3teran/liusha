// retry.go：Generator 装饰器，按 spec §8.5 错误码退避表自动重试 + 切 fallback。
//
// 设计要点：
//   - 装饰器外层嵌套：Router → Retry → Instrument → Provider；
//     Retry 不感知 Instrument，可独立 unit-test。
//   - 错误分类与退避 schedule 是框架能力（fwllm.Classify / fwllm.SleepCtx），
//     本文件只做 Generator 形状的包装与 fallback 编排（业务策略）。
//   - fallback 已由 Router 一次性构造好（不再套 retry，避免双重重试 / 自循环）。
package llm

import (
	"context"
	"time"

	fwllm "github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/config"
)

// RetryOptions 是 spec §8.5 退避表配置（别名到框架层）。
type RetryOptions = fwllm.RetryOptions

// DefaultRetryOptions 返回 spec §8.5 的官方退避表。
var DefaultRetryOptions = fwllm.DefaultRetryOptions

// RetryOptionsFromConfig 把 yaml 配置（秒数列表 + 毫秒）翻译成 RetryOptions。
// 任一字段为 0 由 ApplyDefaults 兜底，调用方可放心透传。
func RetryOptionsFromConfig(c config.RetryConfig) RetryOptions {
	return fwllm.RetryOptions{
		MaxRetries429: c.Max429,
		MaxRetries529: c.Max529,
		MaxRetries5xx: c.Max5xx,
		MaxRetriesNet: c.MaxNet,
		Backoff429:    config.AsDurations(c.Backoff429Seconds),
		Backoff5xx:    config.AsDurations(c.Backoff5xxSeconds),
		BackoffNet:    config.AsDurations(c.BackoffNetSeconds),
		Backoff529:    time.Duration(c.Backoff529Millisec) * time.Millisecond,
	}
}

// retryGen 是 retry 装饰器的 Generator 实现。
// 不与 Router 耦合：fallback 在构造时一次性传入。
type retryGen struct {
	primary  Generator
	fallback Generator // 可空；空表示耗尽即抛错
	opts     fwllm.RetryOptions
}

// WithRetry 返回一个用 RetryOptions 包裹 primary 的 Generator。
// fallback 可为 nil；若非 nil，按 spec §8.5 在 429/529 耗尽时切换调用一次。
//
// 注意：fallback 由调用方（Router）保证未再套 retry，避免双重重试。
func WithRetry(primary Generator, fallback Generator, opts fwllm.RetryOptions) Generator {
	return &retryGen{primary: primary, fallback: fallback, opts: opts}
}

// Provider/Model 透传 primary，便于 Instrument 装饰器读 provider 信息。
// Provider 即使 fallback 兜底也保持 primary 的字符串：路由层面这次调用归属于
// primary 角色（fallback 只是兜底实现细节，按角色统计 token 用量时不应区分）。
func (r *retryGen) Provider() string { return r.primary.Provider() }
func (r *retryGen) Model() string    { return r.primary.Model() }

// Generate 执行 retry 主循环：尝试 primary → 按错误类别决定是否重试 / 切 fallback。
func (r *retryGen) Generate(ctx context.Context, msgs []Message, tools []ToolSchema) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	res, err := r.primary.Generate(ctx, msgs, tools)
	if err == nil {
		return res, nil
	}
	switch fwllm.Classify(err) {
	case fwllm.ClassOther:
		return Result{}, err
	case fwllm.Class429:
		return r.retryLoop(ctx, msgs, tools, err, r.opts.MaxRetries429, r.opts.Backoff429, true)
	case fwllm.Class529:
		return r.retry529(ctx, msgs, tools, err)
	case fwllm.Class5xx:
		return r.retryLoop(ctx, msgs, tools, err, r.opts.MaxRetries5xx, r.opts.Backoff5xx, false)
	case fwllm.ClassNet:
		return r.retryLoop(ctx, msgs, tools, err, r.opts.MaxRetriesNet, r.opts.BackoffNet, false)
	}
	return Result{}, err
}

// retryLoop 通用重试循环：按 schedule sleep 后重试 primary；
// 耗尽后按 useFallback 决定是否切 fallback。
func (r *retryGen) retryLoop(
	ctx context.Context,
	msgs []Message,
	tools []ToolSchema,
	lastErr error,
	maxRetries int,
	schedule []time.Duration,
	useFallback bool,
) (Result, error) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		var d time.Duration
		if attempt < len(schedule) {
			d = schedule[attempt]
		}
		if err := fwllm.SleepCtx(ctx, d); err != nil {
			return Result{}, err
		}
		res, err := r.primary.Generate(ctx, msgs, tools)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if useFallback && r.fallback != nil {
		return r.fallback.Generate(ctx, msgs, tools)
	}
	return Result{}, lastErr
}

// retry529：529 立即重试 N 次，耗尽切 fallback。
func (r *retryGen) retry529(ctx context.Context, msgs []Message, tools []ToolSchema, lastErr error) (Result, error) {
	for attempt := 0; attempt < r.opts.MaxRetries529; attempt++ {
		if err := fwllm.SleepCtx(ctx, r.opts.Backoff529); err != nil {
			return Result{}, err
		}
		res, err := r.primary.Generate(ctx, msgs, tools)
		if err == nil {
			return res, nil
		}
		lastErr = err
	}
	if r.fallback != nil {
		return r.fallback.Generate(ctx, msgs, tools)
	}
	return Result{}, lastErr
}
