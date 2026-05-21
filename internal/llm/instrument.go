// instrument.go：Generator 装饰器，每次 Generate 落 llm_call 行用于成本/路由审计。
//
// 设计要点：
//   - 装饰器模式包裹任意 Generator，不侵入 provider 适配层。
//   - 失败路径仍写库（Error 字段非空），便于故障率统计。
//   - sink.Append 失败仅打 warn 日志，不向上抛 —— 埋点失败不应阻塞业务返回。
//   - CallMeta.RouteKey 写入 llm_invocation.role，按 tracker / commander /
//     striker / inspector 四角色维度聚合成本。
package llm

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/logx"
)

// CallSink 抽象 llm_call 持久化层，便于测试注入 mock。
// 实参为 *llminvocation.Store（其 Append 方法签名一致）。
type CallSink interface {
	Append(ctx context.Context, c llminvocation.Invocation) (int64, error)
}

// CallMeta 是单次 Generate 的上下文标签集，由 runtime 填充。
//
// RouteKey 写入 llm_invocation.role，
// 取值如 "tracker" / "commander" / "striker" / "inspector"，
// 便于按角色维度统计成本和路由生效情况。
type CallMeta struct {
	TaskID    *string
	OwnerType *string // 'passive_session' / 'active_scan'
	OwnerID   *string // passive_session.id / active_scan.id
	RouteKey  string
}

// PricingProvider 抽象成本估算。
// 之所以不直接依赖 observability.Pricing：observability 已 import llm（用 Usage 类型），
// 反向引用会造成 import cycle，故定义最小本地接口。
type PricingProvider interface {
	Estimate(provider, model string, u Usage) float64
}

// instrumentLog 在包级别构建一次，避免每次 Instrument 触发 logx.New
// 内部的全局状态写入（race 风险）。
var instrumentLog = logx.New("llm.instrument")

// Instrument 把 inner 包成自带埋点的 Generator。
// pricing 可为 nil（仅落 usage，不算 cost）。
func Instrument(inner Generator, sink CallSink, meta CallMeta, pricing PricingProvider) Generator {
	return &instrumented{
		inner:   inner,
		sink:    sink,
		meta:    meta,
		pricing: pricing,
		log:     instrumentLog,
	}
}

type instrumented struct {
	inner   Generator
	sink    CallSink
	meta    CallMeta
	pricing PricingProvider
	log     zerolog.Logger
}

// Provider/Model 透传 inner 的实现，便于上层（router/retry）继续读 provider 信息。
func (i *instrumented) Provider() string { return i.inner.Provider() }
func (i *instrumented) Model() string    { return i.inner.Model() }

// Generate 包装单次调用：测延迟 → 调 pricing → 落库。
//
// 即使 inner.Generate 报错，也会写一行带 Error 的 llm_call（不算 cost）。
// sink.Append 自身失败仅打 warn，绝不阻塞业务返回。
func (i *instrumented) Generate(ctx context.Context, msgs []Message, tools []ToolSchema) (Result, error) {
	start := time.Now()
	res, err := i.inner.Generate(ctx, msgs, tools)
	latency := time.Since(start)

	call := llminvocation.Invocation{
		TaskID:    i.meta.TaskID,
		OwnerType: i.meta.OwnerType,
		OwnerID:   i.meta.OwnerID,
		Provider:  i.inner.Provider(),
		Model:        i.inner.Model(),
		InTokens:     res.Usage.InTokens,
		OutTokens:    res.Usage.OutTokens,
		CachedTokens: res.Usage.CachedTokens,
		LatencyMs:    int(latency.Milliseconds()),
		FinishReason: res.FinishReason,
		Role:         i.meta.RouteKey,
	}
	if err != nil {
		call.Error = err.Error()
	} else if i.pricing != nil {
		call.CostUSD = i.pricing.Estimate(i.inner.Provider(), i.inner.Model(), res.Usage)
	}

	// 序列化输入/输出落库（B2 全量审计）。失败仅 warn，不阻塞 Generate 返回。
	//
	// sanitize 修复 LLM 偶发返回非法 JSON 的 ToolCall.Arguments（典型：DeepSeek
	// 截断或带余字符）。json.RawMessage.MarshalJSON 会 validate，遇非法字节直接
	// 整个 marshal 失败，audit JSON 全丢；sanitize 把非法 Arguments 包装成合法
	// {"_raw_invalid":"<原字节>"}，保留可读痕迹。
	if b, mErr := json.Marshal(sanitizeMessages(msgs)); mErr == nil {
		call.Messages = b
	} else {
		i.log.Warn().Err(mErr).Msg("messages marshal 失败（落库回退默认值）")
	}
	if b, mErr := json.Marshal(sanitizeResult(res)); mErr == nil {
		call.Result = b
	} else {
		i.log.Warn().Err(mErr).Msg("result marshal 失败（落库回退默认值）")
	}

	// 埋点用独立的 ctx：业务 ctx 可能因取消而无法落库。
	if _, sinkErr := i.sink.Append(context.Background(), call); sinkErr != nil {
		i.log.Warn().
			Err(sinkErr).
			Str("provider", call.Provider).
			Str("model", call.Model).
			Str("role", call.Role).
			Msg("llm_invocation append 失败（不阻塞 Generate 返回）")
	}
	return res, err
}

// sanitizeMessages 返回 msgs 的浅拷贝，所有 ToolCall.Arguments 经 sanitizeToolCalls 兜底。
// msgs 本身（业务路径）不受影响。
func sanitizeMessages(msgs []Message) []Message {
	if len(msgs) == 0 {
		return msgs
	}
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		if len(m.ToolCalls) > 0 {
			out[i].ToolCalls = sanitizeToolCalls(m.ToolCalls)
		}
	}
	return out
}

// sanitizeResult 返回 res 副本，ToolCall.Arguments 经 sanitizeToolCalls 兜底。
func sanitizeResult(res Result) Result {
	res.ToolCalls = sanitizeToolCalls(res.ToolCalls)
	return res
}

// sanitizeToolCalls 把每个非法 RawMessage 替换为合法 JSON {"_raw_invalid":"<原字节>"}。
// 合法 RawMessage 透传不变。返回浅拷贝避免污染业务路径。
func sanitizeToolCalls(tcs []ToolCall) []ToolCall {
	if len(tcs) == 0 {
		return tcs
	}
	out := make([]ToolCall, len(tcs))
	for i, tc := range tcs {
		out[i] = tc
		if len(tc.Arguments) == 0 || json.Valid(tc.Arguments) {
			continue
		}
		// 包装成合法 JSON 保留原字节痕迹；marshal map[string]string 不会失败。
		wrapped, _ := json.Marshal(map[string]string{"_raw_invalid": string(tc.Arguments)})
		out[i].Arguments = wrapped
	}
	return out
}
