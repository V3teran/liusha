// Package llm 的重试装饰器：按错误码退避表自动重试 + 可选 fallback。
//
// 退避表（spec §8.5）与错误分类是框架能力，定义在本包；
// fallback 链的编排是业务策略，由调用方（Router / internal/llm）装配。
package llm

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"time"
)

// RetryOptions 控制 4 类错误的重试次数和 backoff schedule（spec §8.5）。
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

// ErrorClass 是 4 类可重试错误的判别结果。
type ErrorClass int

// Class429 等枚举定义。
const (
	// ClassOther 等枚举判别结果：决定走哪条退避策略。
	ClassOther ErrorClass = iota // 不重试（含其他 4xx / 业务错误）
	Class429
	Class529
	Class5xx
	ClassNet
)

// Classify 按优先级解析 error：
//  1. *HTTPError 显式类型直接读 Code
//  2. net.Error.Timeout()
//  3. EOF / connection reset / i/o timeout / DNS 解析失败等网络词
//  4. err.Error() 启发式包含状态码（部分 provider 不在 error 结构里暴露 code）
//
// 不能识别的一律 ClassOther（不重试）。
//
// 本函数是全系统网络瞬时错误的**唯一分类事实源**：planner 的重试判定、
// evaluator 的瞬时故障豁免都经 IsTransient 委托到这里——业务侧不再自维护
// 关键词表（曾因三处词表各自漂移，DNS 抖动被误判为不可重试错误）。
func Classify(err error) ErrorClass {
	if err == nil {
		return ClassOther
	}
	var he *HTTPError
	if errors.As(err, &he) {
		return ClassifyCode(he.Code)
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ClassNet
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return ClassNet
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		// 取消不是瞬时故障（调用方主动放弃），重试无意义；
		// 超时是瞬时故障（预算耗尽），换一次机会有意义。
		if errors.Is(err, context.Canceled) {
			return ClassOther
		}
		return ClassNet
	}
	msg := err.Error()
	low := strings.ToLower(msg)
	if strings.Contains(low, "connection reset") ||
		strings.Contains(low, "connection refused") ||
		strings.Contains(low, "connection closed") ||
		strings.Contains(low, "i/o timeout") ||
		strings.Contains(low, "eof") ||
		strings.Contains(low, "dial tcp") ||
		strings.Contains(low, "no such host") ||
		strings.Contains(low, "server misbehaving") ||
		strings.Contains(low, "network unreachable") ||
		strings.Contains(low, "broken pipe") ||
		strings.Contains(low, "tls handshake timeout") {
		return ClassNet
	}
	if strings.Contains(low, "context deadline exceeded") ||
		strings.Contains(low, "timeout") ||
		strings.Contains(low, "deadline exceeded") {
		return ClassNet
	}
	if strings.Contains(msg, "429") {
		return Class429
	}
	if strings.Contains(msg, "529") {
		return Class529
	}
	for _, code := range []string{"500", "502", "503", "504"} {
		if strings.Contains(msg, code) {
			return Class5xx
		}
	}
	return ClassOther
}

// IsTransient 报告错误是否为瞬时故障（网络抖动 / 限流 / 服务端过载 / 超时）——
// 重试有意义。业务侧（planner 重试、evaluator 止损豁免）统一经此判定，
// 不再各自维护关键词表。
func IsTransient(err error) bool {
	return Classify(err) != ClassOther
}

// ClassifyCode 按 HTTP 状态码分类。
func ClassifyCode(code int) ErrorClass {
	switch code {
	case 429:
		return Class429
	case 529:
		return Class529
	case 500, 502, 503, 504:
		return Class5xx
	}
	return ClassOther // 含 400/401/403/404 等其他 4xx
}

// SleepCtx 是支持 ctx 取消的 sleep。d <= 0 时直接返回 nil（仅检查 ctx）。
func SleepCtx(ctx context.Context, d time.Duration) error {
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

// WithRetry 返回带退避重试的 Provider。
func WithRetry(primary Provider, opts RetryOptions) Provider {
	return &retryProvider{primary: primary, opts: opts}
}

type retryProvider struct {
	primary Provider
	opts    RetryOptions
}

func (r *retryProvider) ModelID() string    { return r.primary.ModelID() }
func (r *retryProvider) ProviderID() string { return r.primary.ProviderID() }

// Complete 重试主循环：按错误类别决定退避重试 / 切 fallback。
func (r *retryProvider) Complete(ctx context.Context, req Request) (Response, error) {
	if err := ctx.Err(); err != nil {
		return Response{}, err
	}
	resp, err := r.primary.Complete(ctx, req)
	if err == nil {
		return resp, nil
	}
	switch Classify(err) {
	case ClassOther:
		return Response{}, err
	case Class429:
		return r.retryLoop(ctx, req, err, r.opts.MaxRetries429, r.opts.Backoff429)
	case Class529:
		return r.retry529(ctx, req, err)
	case Class5xx:
		return r.retryLoop(ctx, req, err, r.opts.MaxRetries5xx, r.opts.Backoff5xx)
	case ClassNet:
		return r.retryLoop(ctx, req, err, r.opts.MaxRetriesNet, r.opts.BackoffNet)
	}
	return Response{}, err
}

func (r *retryProvider) retryLoop(ctx context.Context, req Request, lastErr error, maxRetries int, schedule []time.Duration) (Response, error) {
	for attempt := 0; attempt < maxRetries; attempt++ {
		var d time.Duration
		if attempt < len(schedule) {
			d = schedule[attempt]
		}
		if err := SleepCtx(ctx, d); err != nil {
			return Response{}, err
		}
		resp, err := r.primary.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		// 重试后错误类别可能变化（如限流缓解后暴露 400 参数错误）：
		// 非重试类错误立即上抛，不按上一轮的退避表空转。
		if Classify(err) == ClassOther {
			return Response{}, err
		}
		lastErr = err
	}
	return Response{}, lastErr
}

func (r *retryProvider) retry529(ctx context.Context, req Request, lastErr error) (Response, error) {
	for attempt := 0; attempt < r.opts.MaxRetries529; attempt++ {
		if err := SleepCtx(ctx, r.opts.Backoff529); err != nil {
			return Response{}, err
		}
		resp, err := r.primary.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		if Classify(err) == ClassOther {
			return Response{}, err
		}
		lastErr = err
	}
	return Response{}, lastErr
}

// Stream 直通 primary：流式会话中途失败时已产出的事件无法收回，重试语义
// （整体重跑）由调用方决策，重试层不替它决定。
func (r *retryProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	return r.primary.Stream(ctx, req)
}

// CountTokens 走与 Complete 相同的分类策略（一次退避重试）。
func (r *retryProvider) CountTokens(ctx context.Context, req Request) (int, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	n, err := r.primary.CountTokens(ctx, req)
	if err == nil {
		return n, nil
	}
	if Classify(err) == ClassOther {
		return 0, err
	}
	// 退避表由调用方填齐（Router 兜 DefaultRetryOptions）；防御空表取 0 间隔。
	var backoff time.Duration
	if len(r.opts.Backoff5xx) > 0 {
		backoff = r.opts.Backoff5xx[0]
	}
	if err := SleepCtx(ctx, backoff); err != nil {
		return 0, err
	}
	return r.primary.CountTokens(ctx, req)
}
