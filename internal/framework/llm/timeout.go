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
	// dynamic 非空时每次调用经它现读超时（timeout 退为读失败/非正值的回退）——
	// 系统配置（runtime.step_llm_timeout_seconds）热改即生效，无需重启。
	dynamic func(ctx context.Context) time.Duration
}

// WithDynamicCallTimeout 构造「每次调用现读超时」的装饰器：getTimeout 在每次
// Complete/Stream 发起时调用（调用方接多级缓存，命中为主），返回 <=0 时回退
// fallback。runner 单步 LLM 看门狗用此变体——runtime.step_llm_timeout_seconds
// 在前端改完即对本进程后续调用生效。
func WithDynamicCallTimeout(p Provider, fallback time.Duration, getTimeout func(ctx context.Context) time.Duration) Provider {
	if p == nil {
		return p
	}
	return &timeoutProvider{inner: p, timeout: fallback, dynamic: getTimeout}
}

// current 解析本次调用生效的超时预算。
func (p *timeoutProvider) current(ctx context.Context) time.Duration {
	if p.dynamic != nil {
		if d := p.dynamic(ctx); d > 0 {
			return d
		}
	}
	return p.timeout
}

func (p *timeoutProvider) Complete(ctx context.Context, req Request) (Response, error) {
	ctx, cancel := context.WithTimeout(ctx, p.current(ctx))
	defer cancel()
	return p.inner.Complete(ctx, req)
}

func (p *timeoutProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	innerCtx, cancel := context.WithTimeout(ctx, p.current(ctx))
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

func (p *timeoutProvider) ModelID() string    { return p.inner.ModelID() }
func (p *timeoutProvider) ProviderID() string { return p.inner.ProviderID() }
