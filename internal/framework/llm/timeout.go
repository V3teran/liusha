package llm

import (
	"context"
	"time"
)

// WithCallTimeout 返回给 Provider 的每次调用施加独立截止期的装饰器：
// Complete/Stream 各自获得 timeout 的 ctx 预算，超时即失败（Stream 的
// 事件通道随后关闭）。timeout<=0 时不装饰。
//
// 传输层的 http.Client 超时是兜底安全网；本装饰器把预算交给业务侧
// 配置（如单步看门狗），让"一次 LLM 调用永不无限挂死"成为可调的
// 运行时约定——挂死的调用会阻塞调用方的整个事件循环。
type timeoutProvider struct {
	inner   Provider
	timeout time.Duration
}

// WithCallTimeout 构造单调用超时装饰器。
func WithCallTimeout(p Provider, timeout time.Duration) Provider {
	if p == nil || timeout <= 0 {
		return p
	}
	return &timeoutProvider{inner: p, timeout: timeout}
}

func (p *timeoutProvider) Complete(ctx context.Context, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()
	return p.inner.Complete(ctx, req)
}

func (p *timeoutProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	innerCtx, cancel := context.WithTimeout(ctx, p.timeout)
	// innerCtx 超时/取消时底层流关闭；消费方随外层 ctx 放弃时转发也退出（防泄漏）。
	ch, err := p.inner.Stream(innerCtx, req)
	if err != nil {
		cancel()
		return nil, err
	}
	wrapped := make(chan StreamEvent)
	go func() {
		defer close(wrapped)
		defer cancel()
		for ev := range ch {
			select {
			case wrapped <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return wrapped, nil
}

func (p *timeoutProvider) CountTokens(ctx context.Context, req Request) (int, error) {
	return p.inner.CountTokens(ctx, req)
}

func (p *timeoutProvider) ModelID() string    { return p.inner.ModelID() }
func (p *timeoutProvider) ProviderID() string { return p.inner.ProviderID() }
