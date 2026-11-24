// usage.go — Provider 调用埋点（InstrumentProvider → UsageSink）。
//
// 权威口径（llminvocation 表约定）：每次 Complete 调用（含失败）落一行，
// 供 token 用量 / 路由 / 延迟审计。装饰器只依赖本包类型 + 最小 Sink 接口，
// 业务侧（runner 组件）用适配器接到 llminvocation.Store——框架不 import 业务包。
//
// 装饰点：Router 装配 Provider 时包在最外层（retry 之外），
// 记录"调用方视角"的一次调用；逐次重试的中间态不落库。

package llm

import (
	"context"
	"encoding/json"
	"errors"
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
	// Err 是失败正文（给人读）；ErrClass 是它的分类，取自真实错误（给筛选用）。两者分开
	// 携带：只留文本的话，落库一方拿文本重新分类时哨兵错误已经认不出来——本地的在途
	// 名额节流会被判成「其它错误」。成功调用两者皆为空。
	Err            string
	ErrClass       ErrorClass
	Role           string // 调用者角色（planner/executor/monitor/chat 等），空 = 未分类
	TaskID         string // 归属任务，空 = 任务外调用
	AgentRunID     string // 归属认知轮次的 agent_run.id，空 = 未接通（历史行恒空）
	ConversationID string // 对话内直接发起的调用所在的对话，空 = 智能体运行的调用或无对话
	Messages       []byte // jsonb：输入消息数组（审计回放用）
	Result         []byte // jsonb：完整响应（非流式才有）
}

// UsageSink 抽象审计落库层。
type UsageSink interface {
	RecordUsage(ctx context.Context, r UsageRecord)
}

// CallMeta 是调用方通过 ctx 携带的审计标签（task 归属 + 轮次归属 + 角色维度）。
type CallMeta struct {
	TaskID     string
	AgentRunID string // 本轮认知循环的 agent_run.id（llm_invocation.agent_run_id 外键取值）
	// ConversationID 是对话里直接发起的调用（问答、意图分类、标题生成）所在的对话；
	// 智能体运行的调用不填，经 TaskID 找对话。
	ConversationID string
	Role           string
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
	// 只记成功等于审计在最需要的地方失明。没有发往上游的调用（额度窗口内被挡下、
	// 排不到在途名额）是例外：它没有现场可留，而且每逢额度窗口或账户满载就会来一次，
	// 留存只是把提示词反复写进审计。
	if !isHeld(err) {
		rec.Messages = auditMessages(req.Messages)
	}
	if err != nil {
		rec = failRecord(rec, err)
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
		rec = failRecord(rec, err)
		if !isHeld(err) {
			rec.Messages = auditMessages(req.Messages)
		}
		p.sink.RecordUsage(ctx, rec)
		return ch, err
	}

	out := make(chan StreamEvent, 16)
	go func() {
		defer close(out)
		var (
			usage Usage
			// 影子聚合：与 Complete 路径同构的 Response——流式调用的审计 Result
			// 由此而来（content/tool_calls/thinking 原文不再缺失）。
			shadow  Response
			failure *streamFailure // nil = 尚未失败
			done    bool           // 收到 StreamDone 收尾事件
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
				failure = newStreamFailure(ev.Err)
			}
			// 消费方经 ctx 放弃（如任务中止/超时）时不能傻等发送：等待会让本
			// goroutine 永久阻塞（消费侧 CollectStream 提前返回后无人再读），
			// 审计记录也随之丢失。放弃路径仍按失败收尾落库。
			select {
			case out <- ev:
			case <-ctx.Done():
				if failure == nil {
					failure = abandoned(ctx.Err())
				}
				p.sink.RecordUsage(ctx, p.finishStreamRecord(meta, start, req, usage, shadow, failure))
				return
			}
		}
		// 未见收尾事件即关闭：部分正文不得审计为成功（与 CollectStream 同口径）。
		// 消费方经 ctx 放弃（任务中止/停机）时 provider 直接关通道、不发收尾事件——
		// 这不是截断，先按取消归因，与上面 select 分支的 abandoned 同口径。
		if failure == nil && !done {
			if err := ctx.Err(); err != nil {
				failure = abandoned(err)
			} else {
				failure = newStreamFailure(ErrStreamTruncated)
			}
		}
		p.sink.RecordUsage(ctx, p.finishStreamRecord(meta, start, req, usage, shadow, failure))
	}()
	return out, nil
}

// streamFailure 是流式调用失败时的收尾：finish 落 finish_reason 列（终止原因），
// message 落 error_message 列（错误正文），cause 是供分类的原因错误（nil = 没有可分类的
// 真实错误）。
type streamFailure struct {
	finish  string
	message string
	cause   error
}

// newStreamFailure 以流上给出的错误收尾；err 为 nil 时上游没有说明原因。
func newStreamFailure(err error) *streamFailure {
	if err == nil {
		return &streamFailure{finish: "error", message: "stream error (no detail)"}
	}
	return &streamFailure{finish: "error: " + err.Error(), message: err.Error(), cause: err}
}

// abandoned 以「消费方经 ctx 放弃了这条流」收尾，原因取 ctx 的错误。
func abandoned(err error) *streamFailure {
	message := "stream abandoned: " + err.Error()
	return &streamFailure{finish: "error: " + message, message: message, cause: err}
}

// finishStreamRecord 把流式调用的影子聚合折成审计记录（成功收尾与放弃/错误
// 收尾共用一个落库口径）。
func (p *instrumentedProvider) finishStreamRecord(
	meta CallMeta, start time.Time, req Request,
	usage Usage, shadow Response, failure *streamFailure,
) UsageRecord {
	rec := p.baseRecord(meta)
	rec.LatencyMs = int(time.Since(start).Milliseconds())
	rec.InTokens = usage.InTokens
	rec.OutTokens = usage.OutTokens
	rec.CachedTokens = usage.CachedTokens
	if failure != nil {
		// 流上错误同时落 error_message：失败调用的查询口径统一为 error_message <> ''
		// （此前流错误只在 finish_reason、Complete 错误只在 error_message，「查全部失败」要
		// OR 两列）。finish_reason 保留 "error: ..." 原值，兼容既有按列查询。
		rec.FinishReason = failure.finish
		rec.Err = failure.message
		rec.ErrClass = Classify(failure.cause)
	} else {
		// 成功收尾的终止原因也写列：只进 shadow jsonb 会让 finish_reason
		// 列在流式路径恒空（实测 197 行里 164 行空串），按列筛选成功调用
		// 永远查不到；错误（"error: ..."）与成功（"stop"）同列可辨。
		rec.FinishReason = "stop"
		shadow.Usage = usage
		shadow.FinishReason = "stop"
		rec.Result = auditResponse(shadow)
	}
	rec.Messages = auditMessages(req.Messages)
	return rec
}

// failRecord 在审计记录上记下一次失败：正文给人读，分类给筛选用。分类取自真实错误——
// 只留文本的话，落库一方拿文本重新分类时哨兵错误已经认不出来。
func failRecord(rec UsageRecord, err error) UsageRecord {
	rec.Err = err.Error()
	rec.ErrClass = Classify(err)
	return rec
}

func (p *instrumentedProvider) ModelID() string    { return p.inner.ModelID() }
func (p *instrumentedProvider) ProviderID() string { return p.inner.ProviderID() }

// isHeld 报告这次调用是否在额度窗口内被挡下、没有发往上游。
func isHeld(err error) bool {
	var he *heldError
	return errors.As(err, &he)
}

func (p *instrumentedProvider) baseRecord(meta CallMeta) UsageRecord {
	return UsageRecord{
		Provider:       p.inner.ProviderID(),
		Model:          p.inner.ModelID(),
		Role:           meta.Role,
		TaskID:         meta.TaskID,
		AgentRunID:     meta.AgentRunID,
		ConversationID: meta.ConversationID,
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
