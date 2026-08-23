// retry.go：Generator 装饰器，按 spec §8.5 错误码退避表自动重试 + 切 fallback。
//
// 设计要点：
//   - 装饰器外层嵌套：Router → Retry → Instrument → Provider；
//     Retry 不感知 Instrument，可独立 unit-test。
//   - 错误识别优先 *HTTPError 显式类型；不可解析时回退启发式（err.Error() 含状态码字符串）。
//   - fallback 已由 Router 一次性构造好（不再套 retry，避免双重重试 / 自循环）。
//   - backoff 用 ctx.Done 取消，避免 ctx 已 done 仍 sleep。
package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/config"
)

// HTTPError 是上游 HTTP 错误的统一表示。
// provider 适配层可在拿到非 2xx 时构造此错误（推荐 Inner 包裹原 error 便于排查）。
type HTTPError struct {
	Code  int
	Inner error
}

// Error 满足 error 接口。
func (e *HTTPError) Error() string {
	if e.Inner != nil {
		return fmt.Sprintf("http %d: %v", e.Code, e.Inner)
	}
	return fmt.Sprintf("http %d", e.Code)
}

// Unwrap 让 errors.Is/As 能穿透。
func (e *HTTPError) Unwrap() error { return e.Inner }

// RetryOptions 控制 4 类错误的重试次数和 backoff schedule（spec §8.5）。
//
// 默认值：
//   - 429 重试 3 次，1s/4s/16s 指数退避，耗尽切 fallback
//   - 529 重试 1 次（立即），耗尽切 fallback
//   - 5xx (500/502/503/504) 重试 2 次 1s/4s，不切 fallback
//   - 网络超时 / connection reset 重试 2 次 1s/3s，不切 fallback
//   - 其他 4xx (400/401/403/404) 不重试直接抛
type RetryOptions struct {
	MaxRetries429 int
	MaxRetries529 int
	MaxRetries5xx int
	MaxRetriesNet int

	Backoff429 []time.Duration // 长度需 ≥ MaxRetries429
	Backoff5xx []time.Duration // 长度需 ≥ MaxRetries5xx
	BackoffNet []time.Duration // 长度需 ≥ MaxRetriesNet
	Backoff529 time.Duration   // 单值（529 只重试 1 次）
}

// DefaultRetryOptions 返回 spec §8.5 的官方退避表。
func DefaultRetryOptions() RetryOptions {
	return RetryOptions{
		MaxRetries429: 3,
		MaxRetries529: 1,
		MaxRetries5xx: 2,
		MaxRetriesNet: 2,
		Backoff429:    []time.Duration{1 * time.Second, 4 * time.Second, 16 * time.Second},
		Backoff5xx:    []time.Duration{1 * time.Second, 4 * time.Second},
		BackoffNet:    []time.Duration{1 * time.Second, 3 * time.Second},
		Backoff529:    0, // 立即重试
	}
}

// RetryOptionsFromConfig 把 yaml 配置（秒数列表 + 毫秒）翻译成 RetryOptions。
// 任一字段为 0 由 ApplyDefaults 兜底，调用方可放心透传。
func RetryOptionsFromConfig(c config.RetryConfig) RetryOptions {
	return RetryOptions{
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

// errorClass 是 4 类可重试错误的判别结果。
type errorClass int

const (
	classOther errorClass = iota // 不重试（含其他 4xx / 业务错误）
	class429
	class529
	class5xx
	classNet
)

// classify 按优先级解析 error：
//  1. *HTTPError 显式类型直接读 Code
//  2. net.Error.Timeout()
//  3. err.Error() 包含 "connection reset" / "i/o timeout" 等网络词
//  4. err.Error() 字符串启发式包含 "429" / "529" / "500" 等
//
// 不能识别的一律 classOther（不重试）。
func classify(err error) errorClass {
	if err == nil {
		return classOther
	}
	// 1. 显式 HTTPError
	var he *HTTPError
	if errors.As(err, &he) {
		return classifyCode(he.Code)
	}
	// 2. 网络超时
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return classNet
	}
	// 3. connection reset / refused / i/o timeout
	msg := err.Error()
	low := strings.ToLower(msg)
	if strings.Contains(low, "connection reset") ||
		strings.Contains(low, "connection refused") ||
		strings.Contains(low, "i/o timeout") {
		return classNet
	}
	// 4. 启发式：err.Error() 含状态码（不优雅但部分 provider 不在 error 结构里暴露 code）
	if strings.Contains(msg, "429") {
		return class429
	}
	if strings.Contains(msg, "529") {
		return class529
	}
	for _, code := range []string{"500", "502", "503", "504"} {
		if strings.Contains(msg, code) {
			return class5xx
		}
	}
	return classOther
}

// classifyCode 按 HTTP 状态码分类。
func classifyCode(code int) errorClass {
	switch code {
	case 429:
		return class429
	case 529:
		return class529
	case 500, 502, 503, 504:
		return class5xx
	}
	return classOther // 含 400/401/403/404 等其他 4xx
}

// retryGen 是 retry 装饰器的 Generator 实现。
// 不与 Router 耦合：fallback 在构造时一次性传入。
type retryGen struct {
	primary  Generator
	fallback Generator // 可空；空表示耗尽即抛错
	opts     RetryOptions
}

// WithRetry 返回一个用 RetryOptions 包裹 primary 的 Generator。
// fallback 可为 nil；若非 nil，按 spec §8.5 在 429/529 耗尽时切换调用一次。
//
// 注意：fallback 由调用方（Router）保证未再套 retry，避免双重重试。
func WithRetry(primary Generator, fallback Generator, opts RetryOptions) Generator {
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
	cls := classify(err)
	switch cls {
	case classOther:
		return Result{}, err
	case class429:
		return r.retryLoop(ctx, msgs, tools, err, r.opts.MaxRetries429, r.opts.Backoff429, true)
	case class529:
		return r.retry529(ctx, msgs, tools, err)
	case class5xx:
		return r.retryLoop(ctx, msgs, tools, err, r.opts.MaxRetries5xx, r.opts.Backoff5xx, false)
	case classNet:
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
		if err := sleepCtx(ctx, d); err != nil {
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
		if err := sleepCtx(ctx, r.opts.Backoff529); err != nil {
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

// sleepCtx 是支持 ctx 取消的 sleep。d <= 0 时直接返回 nil（仅检查 ctx）。
func sleepCtx(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
