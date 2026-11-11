// usage.go — Provider 调用埋点（InstrumentProvider → UsageSink）。
//
// 权威口径（llminvocation 表约定）：每次 Complete 调用（含失败）落一行，
// 供 token 用量 / 路由 / 延迟审计。装饰器只依赖本包类型 + 最小 Sink 接口，
// 业务侧（cmd/runner）用适配器接到 llminvocation.Store——框架不 import 业务包。
//
// 装饰点：Router 装配 Provider 时包在最外层（retry 之外），
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
	FinishReason string
	Err          string
	Role         string // 调用者角色（planner/executor/monitor/chat 等），空 = 未分类
	TaskID       string // 归属任务，空 = 任务外调用
	AgentRunID   string // 归属认知轮次的 agent_run.id，空 = 未接通（历史行恒空）
	Messages     []byte // jsonb：输入消息数组（审计回放用）
	Result       []byte // jsonb：完整响应（非流式才有）
}

// UsageSink 抽象审计落库层。
type UsageSink interface {
	RecordUsage(ctx context.Context, r UsageRecord)
}

// CallMeta 是调用方通过 ctx 携带的审计标签（task 归属 + 轮次归属 + 角色维度）。
type CallMeta struct {
	TaskID     string
	AgentRunID string // 本轮认知循环的 agent_run.id（llm_invocation.agent_run_id 外键取值）
	Role       string
}

type ctxKeyCallMeta struct{}

// WithCallMeta 把审计标签挂到 ctx（随调用链透传）。
func WithCallMeta(ctx context.Context, meta CallMeta) context.Context {
	return context.WithValue(ctx, ctxKeyCallMeta{}, meta)
}

// CallMetaFromContext 取审计标签（未设置返回零值）。
func CallMetaFromContext(ctx context.Context) CallMeta {
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
	meta := CallMetaFromContext(ctx)
	start := time.Now()

	resp, err := p.inner.Complete(ctx, req)

	rec := p.baseRecord(meta)
	rec.LatencyMs = int(time.Since(start).Milliseconds())
	// messages 无论成败都留存：失败现场（哪些消息触发 4xx）是排障第一证据，
	// 只记成功等于审计在最需要的地方失明。
	rec.Messages = auditMessages(req.Messages)
	if err != nil {
		rec.Err = err.Error()
	} else {
		rec.InTokens = resp.Usage.InTokens
		rec.OutTokens = resp.Usage.OutTokens
		rec.CachedTokens = resp.Usage.CachedTokens
		rec.FinishReason = resp.FinishReason
		rec.Result = auditResponse(resp)
	}
	p.sink.RecordUsage(ctx, rec)
	return resp, err
}

func (p *instrumentedProvider) Stream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	meta := CallMetaFromContext(ctx)
	start := time.Now()

	ch, err := p.inner.Stream(ctx, req)
	if err != nil {
		rec := p.baseRecord(meta)
		rec.LatencyMs = int(time.Since(start).Milliseconds())
		rec.Err = err.Error()
		rec.Messages = auditMessages(req.Messages)
		p.sink.RecordUsage(ctx, rec)
		return ch, err
	}

	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		var (
			usage  Usage
			finish string
			// 影子聚合：与 Complete 路径同构的 Response——流式调用的审计 Result
			// 由此而来（content/tool_calls/thinking 原文不再缺失）。
			shadow      Response
			failed      bool
			done        bool // 收到 StreamDone 收尾事件
			streamError string
		)
		for ev := range ch {
			switch ev.Kind {
			case StreamText:
				shadow.Content += ev.Content
			case StreamThinking:
				if ev.Content != "" {
					if shadow.Thinking == nil {
						shadow.Thinking = &ThinkingBlock{}
					}
					shadow.Thinking.Text += ev.Content
				}
			case StreamToolCall:
				if ev.Tool != nil {
					shadow.ToolCalls = append(shadow.ToolCalls, *ev.Tool)
				}
			case StreamDone:
				done = true
				if ev.Usage != nil {
					usage = *ev.Usage
				}
				if shadow.Thinking != nil {
					shadow.Thinking.Signature = ev.Signature
				}
			case StreamError:
				failed = true
				if ev.Err != nil {
					streamError = ev.Err.Error()
					finish = "error: " + streamError
				} else {
					streamError = "stream error (no detail)"
					finish = "error"
				}
			}
			// 消费方经 ctx 放弃（如任务中止/超时）时不能傻等发送：等待会让本
			// goroutine 永久阻塞（消费侧 CollectStream 提前返回后无人再读），
			// 审计记录也随之丢失。放弃路径仍按失败收尾落库。
			select {
			case out <- ev:
			case <-ctx.Done():
				if !failed {
					failed = true
					streamError = "stream abandoned: " + ctx.Err().Error()
					finish = "error: " + streamError
				}
				p.sink.RecordUsage(ctx, p.finishStreamRecord(meta, start, req, usage, shadow, failed, finish, streamError))
				return
			}
		}
		// 未见收尾事件即关闭：部分正文不得审计为成功（与 CollectStream 同口径）。
		if !failed && !done {
			failed = true
			streamError = ErrStreamTruncated.Error()
			finish = "error: " + streamError
		}
		rec := p.finishStreamRecord(meta, start, req, usage, shadow, failed, finish, streamError)
		p.sink.RecordUsage(ctx, rec)
	}()
	return out, nil
}

// finishStreamRecord 把流式调用的影子聚合折成审计记录（成功收尾与放弃/错误
// 收尾共用一个落库口径）。
func (p *instrumentedProvider) finishStreamRecord(
	meta CallMeta, start time.Time, req Request,
	usage Usage, shadow Response, failed bool, finish, streamError string,
) UsageRecord {
	rec := p.baseRecord(meta)
	rec.LatencyMs = int(time.Since(start).Milliseconds())
	rec.InTokens = usage.InTokens
	rec.OutTokens = usage.OutTokens
	rec.CachedTokens = usage.CachedTokens
	if !failed {
		// 成功收尾的终止原因也写列：只进 shadow jsonb 会让 finish_reason
		// 列在流式路径恒空（实测 197 行里 164 行空串），按列筛选成功调用
		// 永远查不到；错误（"error: ..."）与成功（"stop"）同列可辨。
		finish = "stop"
	} else {
		// 流上错误同时落 error_message：失败调用的查询口径统一为
		// error_message <> ''（此前流错误只在 finish_reason、Complete
		// 错误只在 error_message，「查全部失败」要 OR 两列）。finish_reason
		// 保留 "error: ..." 原值，兼容既有按列查询。
		rec.Err = streamError
	}
	rec.FinishReason = finish
	rec.Messages = auditMessages(req.Messages)
	if !failed {
		shadow.Usage = usage
		shadow.FinishReason = finish
		rec.Result = auditResponse(shadow)
	}
	return rec
}

func (p *instrumentedProvider) ModelID() string    { return p.inner.ModelID() }
func (p *instrumentedProvider) ProviderID() string { return p.inner.ProviderID() }

func (p *instrumentedProvider) baseRecord(meta CallMeta) UsageRecord {
	return UsageRecord{
		Provider:   p.inner.ProviderID(),
		Model:      p.inner.ModelID(),
		Role:       meta.Role,
		TaskID:     meta.TaskID,
		AgentRunID: meta.AgentRunID,
	}
}

// auditMessages / auditResponse 序列化审计载荷。模型可能吐出残缺的工具参数（流式截断等），
// 原样序列化会因非法 json.RawMessage 失败、审计落成空值——偏偏是最需要现场的时候。
// 这里把非法参数改存为 JSON 字符串：原文保留，序列化必成功。
func auditMessages(msgs []Message) []byte {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		m.ToolCalls = auditToolCalls(m.ToolCalls)
		out[i] = m
	}
	return auditJSON(out)
}

func auditResponse(resp Response) []byte {
	resp.ToolCalls = auditToolCalls(resp.ToolCalls)
	return auditJSON(resp)
}

func auditToolCalls(tcs []ToolCall) []ToolCall {
	if len(tcs) == 0 {
		return tcs
	}
	out := make([]ToolCall, len(tcs))
	for i, tc := range tcs {
		if len(tc.Arguments) > 0 && !json.Valid(tc.Arguments) {
			quoted, _ := json.Marshal(string(tc.Arguments)) // 字符串序列化不会失败
			tc.Arguments = quoted
		}
		out[i] = tc
	}
	return out
}

// auditJSON 序列化审计载荷；工具参数已规整后仍失败属于类型层面的编程错误，
// 落一条可见的占位而不是静默空值。
func auditJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		b, _ = json.Marshal(map[string]string{"audit_marshal_error": err.Error()})
	}
	return b
}
