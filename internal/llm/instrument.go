// instrument.go：Generator 装饰器，每次 Generate 落 llm_call 行用于成本/路由审计。
//
// 设计要点：
//   - 装饰器模式包裹任意 Generator，不侵入 provider 适配层。
//   - 失败路径仍写库（Error 字段非空），便于故障率统计。
//   - sink.Append 失败仅打 warn 日志，不向上抛 —— 埋点失败不应阻塞业务返回。
//   - CallMeta.RouteKey（黑客松借鉴）写入 llm_call.role，按 react.main /
//     observer / distill / compaction / vision 维度聚合成本（spec §8.2 + part3 §借鉴增量）。
package llm

import (
	"context"
	"encoding/json"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/llmcall"
	"github.com/V3teran/liusha/internal/logx"
)

// CallSink 抽象 llm_call 持久化层，便于测试注入 mock。
// 实参为 *llmcall.Store（其 Append 方法签名一致）。
type CallSink interface {
	Append(ctx context.Context, c llmcall.Call) (int64, error)
}

// CallMeta 是单次 Generate 的上下文标签集，由 runtime 填充。
//
// RouteKey 为黑客松借鉴字段：写入 llm_call.role，
// 取值如 "orchestrator" / "prober" / "observer" / "distill" / "vision"，
// 便于按角色维度统计成本和路由生效情况。
type CallMeta struct {
	TaskID       *string
	EngagementID *string
	RouteKey     string
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

	call := llmcall.Call{
		TaskID:       i.meta.TaskID,
		EngagementID: i.meta.EngagementID,
		Provider:     i.inner.Provider(),
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
	if b, mErr := json.Marshal(msgs); mErr == nil {
		call.MessagesJSON = b
	} else {
		i.log.Warn().Err(mErr).Msg("messages_json marshal 失败（落库回退默认值）")
	}
	if b, mErr := json.Marshal(res); mErr == nil {
		call.ResultJSON = b
	} else {
		i.log.Warn().Err(mErr).Msg("result_json marshal 失败（落库回退默认值）")
	}

	// 埋点用独立的 ctx：业务 ctx 可能因取消而无法落库。
	if _, sinkErr := i.sink.Append(context.Background(), call); sinkErr != nil {
		i.log.Warn().
			Err(sinkErr).
			Str("provider", call.Provider).
			Str("model", call.Model).
			Str("role", call.Role).
			Msg("llm_call append 失败（不阻塞 Generate 返回）")
	}
	return res, err
}
