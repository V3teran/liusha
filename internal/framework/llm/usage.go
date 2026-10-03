// usage.go — Provider 调用埋点（UsageRecorder）。
//
// 权威口径（llminvocation 表约定）：每次 Complete/Stream 调用（含失败）落一行，
// 供 token 用量 / 路由 / 延迟审计。装饰器只依赖本包类型 + 最小 Sink 接口，
// 业务侧（cmd/runner）用适配器接到 llminvocation.Store——框架不 import 业务包。
//
// 装饰点：Router 装配 Provider 时包在最外层（retry/fallback 之外），
// 记录"调用方视角"的一次调用；逐次重试的中间态不落库。
package llm

import (
	"context"
	"encoding/json"
	"time"
)

// UsageRecord 是一次 LLM 调用的审计记录。
type UsageRecord struct {
	Provider     string
	Model        string
	InTokens     int
	OutTokens    int
	CachedTokens int
	LatencyMs    int
	TTFTMs       int // 仅流式非 0
	IsStream     bool
	FinishReason string
	Err          string
	Role         string // 调用者角色（planner/executor/monitor/chat 等），空 = 未分类
	TaskID       string // 归属任务，空 = 任务外调用
	Messages     []byte // jsonb：输入消息数组（审计回放用）
	Result       []byte // jsonb：完整响应（非流式才有）
}

// UsageSink 抽象审计落库层。
type UsageSink interface {
	RecordUsage(ctx context.Context, r UsageRecord)
}

// CallMeta 是调用方通过 ctx 携带的审计标签（task 归属 + 角色维度）。
type CallMeta struct {
	TaskID string
	Role   string
}

type ctxKeyCallMeta struct{}

// WithCallMeta 把审计标签挂到 ctx（随调用链透传）。
func WithCallMeta(ctx context.Context, meta CallMeta) context.Context {
	return context.WithValue(ctx, ctxKeyCallMeta{}, meta)
}

// CallMetaFromCtx 取审计标签（未设置返回零值）。
func CallMetaFromCtx(ctx context.Context) CallMeta {
	if m, ok := ctx.Value(ctxKeyCallMeta{}).(CallMeta); ok {
		return m
	}
	return CallMeta{}
}

// InstrumentProvider 给 Provider 织入调用埋点。sink 为 nil 时原样返回。
func InstrumentProvider(p Provider, sink UsageSink) Provider {
	if sink == nil {
		return p
	}
	return &instrumentedProvider{inner: p, sink: sink}
}

type instrumentedProvider struct {
	inner Provider
	sink  UsageSink
}

func (p *instrumentedProvider) Complete(ctx context.Context, req Request) (Response, error) {
	meta := CallMetaFromCtx(ctx)
	start := time.Now()

	resp, err := p.inner.Complete(ctx, req)

	rec := p.baseRecord(meta, false)
	rec.LatencyMs = int(time.Since(start).Milliseconds())
	if err != nil {
		rec.Err = err.Error()
	} else {
		rec.InTokens = resp.Usage.InTokens
		rec.OutTokens = resp.Usage.OutTokens
		rec.CachedTokens = resp.Usage.CachedTokens
		rec.FinishReason = resp.FinishReason
		rec.Messages, _ = json.Marshal(req.Messages)
		rec.Result, _ = json.Marshal(resp)
	}
	p.sink.RecordUsage(ctx, rec)
	return resp, err
}

func (p *instrumentedProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	meta := CallMetaFromCtx(ctx)
	start := time.Now()

	ch, err := p.inner.Stream(ctx, req)
	if err != nil {
		rec := p.baseRecord(meta, true)
		rec.LatencyMs = int(time.Since(start).Milliseconds())
		rec.Err = err.Error()
		p.sink.RecordUsage(ctx, rec)
		return ch, err
	}

	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		var (
			ttft   int
			usage  Usage
			finish string
		)
		for ev := range ch {
			if ttft == 0 && ev.Kind == StreamText {
				ttft = int(time.Since(start).Milliseconds())
			}
			if ev.Kind == StreamDone && ev.Usage != nil {
				usage = *ev.Usage
			}
			if ev.Kind == StreamError {
				if ev.Err != nil {
					finish = "error: " + ev.Err.Error()
				} else {
					finish = "error"
				}
			}
			out <- ev
		}
		rec := p.baseRecord(meta, true)
		rec.LatencyMs = int(time.Since(start).Milliseconds())
		rec.TTFTMs = ttft
		rec.InTokens = usage.InTokens
		rec.OutTokens = usage.OutTokens
		rec.CachedTokens = usage.CachedTokens
		rec.FinishReason = finish
		rec.Messages, _ = json.Marshal(req.Messages)
		p.sink.RecordUsage(ctx, rec)
	}()
	return out, nil
}

func (p *instrumentedProvider) CountTokens(ctx context.Context, req Request) (int, error) {
	return p.inner.CountTokens(ctx, req)
}

func (p *instrumentedProvider) ModelID() string    { return p.inner.ModelID() }
func (p *instrumentedProvider) ProviderID() string { return p.inner.ProviderID() }

func (p *instrumentedProvider) baseRecord(meta CallMeta, isStream bool) UsageRecord {
	return UsageRecord{
		Provider: p.inner.ProviderID(),
		Model:    p.inner.ModelID(),
		IsStream: isStream,
		Role:     meta.Role,
		TaskID:   meta.TaskID,
	}
}
