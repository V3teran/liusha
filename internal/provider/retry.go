// Package provider — 重试装饰器。
//
// 架构规格：最多 3 次，指数退避 1s/2s/4s。5xx 可重试，4xx / cancel 不重试。
package provider

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"
)

type retryProvider struct {
	inner  Provider
	config RetryConfig
}

// WithRetry 返回包裹了自动重试的 Provider。
func WithRetry(p Provider, cfg RetryConfig) Provider {
	if cfg.Max <= 0 {
		cfg.Max = 3
	}
	if len(cfg.Backoffs) == 0 {
		cfg.Backoffs = []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}
	}
	return &retryProvider{inner: p, config: cfg}
}

func (r *retryProvider) ModelID() string    { return r.inner.ModelID() }
func (r *retryProvider) ProviderID() string { return r.inner.ProviderID() }

func (r *retryProvider) Complete(ctx context.Context, req Request) (Response, error) {
	var lastErr error
	for attempt := 0; attempt <= r.config.Max; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, r.backoff(attempt-1)); err != nil {
				return Response{}, err
			}
		}
		resp, err := r.inner.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		if retryErr := isRetryable(err); retryErr != nil {
			return Response{}, retryErr
		}
		lastErr = err
	}
	return Response{}, lastErr
}

// Stream 不在重试层包装：流式调用建立连接后出错由 Actor 层决策是否重跑整个 Step。
func (r *retryProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	return r.inner.Stream(ctx, req)
}

func (r *retryProvider) CountTokens(ctx context.Context, req Request) (int, error) {
	var lastErr error
	for attempt := 0; attempt <= r.config.Max; attempt++ {
		if attempt > 0 {
			if err := sleepCtx(ctx, r.backoff(attempt-1)); err != nil {
				return 0, err
			}
		}
		n, err := r.inner.CountTokens(ctx, req)
		if err == nil {
			return n, nil
		}
		if retryErr := isRetryable(err); retryErr != nil {
			return 0, retryErr
		}
		lastErr = err
	}
	return 0, lastErr
}

func (r *retryProvider) backoff(attempt int) time.Duration {
	if attempt < len(r.config.Backoffs) {
		return r.config.Backoffs[attempt]
	}
	return r.config.Backoffs[len(r.config.Backoffs)-1]
}

// isRetryable 返回 nil 表示可重试，返回非 nil error 表示不可重试（直接抛出该 error）。
func isRetryable(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var he *HTTPError
	if errors.As(err, &he) {
		if he.Code >= 500 {
			return nil // 可重试
		}
		return err // 4xx 不重试
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "i/o timeout") {
		return nil
	}
	return err
}

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
