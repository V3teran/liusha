// Package observability 提供成本核算与遥测相关的工具。
//
// pricing.go 内置 spec §8.2 的 LLM 单价表，按 (provider, model, usage)
// 计算单次调用的 USD 成本。
//
// cached_tokens 在不同 provider 的语义不同：
//   - Anthropic: cache_read_input_tokens 与 input_tokens 独立返回（"加项"）
//     cost = in/M * in_price + out/M * out_price + cached/M * in_price * cache_discount
//   - OpenAI / DeepSeek: prompt_tokens_details.cached_tokens ⊆ prompt_tokens（"子集"）
//     cost = (in-cached)/M * in_price + cached/M * in_price * cache_discount + out/M * out_price
//
// 由 ModelPrice.CachedInIn 控制：true=子集语义，false=加项语义。
package observability

import (
	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/llm"
)

// ModelPrice 是某个 provider/model 组合的单价定义。
// InputPerMUSD / OutputPerMUSD 单位为 USD per 1M tokens。
// CacheDiscount 为 input 缓存命中部分相对原价的折扣系数（例如 0.10 表示 10% 原价）。
//
// CachedInIn 标记 Usage.CachedTokens 是否已被计入 InTokens：
//   - true（OpenAI/DeepSeek）：CachedTokens ⊆ InTokens；Estimate 先减再分别计价。
//   - false（Anthropic 默认）：CachedTokens 与 InTokens 独立返回；Estimate 加项处理。
type ModelPrice struct {
	InputPerMUSD  float64
	OutputPerMUSD float64
	CacheDiscount float64
	CachedInIn    bool
}

// Pricing 是 ModelPrice 的查找表 + 成本估算实现。
type Pricing struct {
	table map[string]ModelPrice
}

// tokensPerMillion 是 1M tokens 的归一化分母，避免在公式里散落魔法数。
const tokensPerMillion = 1_000_000.0

// NewPricing 用 yaml 配置构造 Pricing。
//
// 设计意图：单价单位/折扣/缓存语义全部从 config.PricingConfig 注入，
// 让运维不重编即可维护单价表（provider 季度降价 / 加新模型 / 跨环境差异）。
// caller 通常这样用：`pricing := observability.NewPricing(cfg.Pricing)` 一次构造，
// 透传给 llm.Instrument / handler.pricing 字段；后续 Lookup/Estimate 0 IO 命中内存表。
//
// 入参 c 字段为空时由 config.ApplyDefaults 兜底（包含 spec §8.2 三模型基准）。
func NewPricing(c config.PricingConfig) Pricing {
	table := make(map[string]ModelPrice, len(c.Models))
	for k, m := range c.Models {
		table[k] = ModelPrice{
			InputPerMUSD:  m.InputPerMUSD,
			OutputPerMUSD: m.OutputPerMUSD,
			CacheDiscount: m.CacheDiscount,
			CachedInIn:    m.CachedInIn,
		}
	}
	return Pricing{table: table}
}

// Lookup 按 (provider, model) 返回 ModelPrice；未命中返回 (zero, false)。
func (p Pricing) Lookup(provider, model string) (ModelPrice, bool) {
	mp, ok := p.table[provider+"/"+model]
	return mp, ok
}

// Estimate 计算单次调用的 USD 成本。
// 未命中单价表时返回 0（不报错，由上层决定是否记录 warning）。
//
// CachedInIn=true 时（OpenAI/DeepSeek）：cached ⊆ in_tokens，先减再分别计价；
// CachedInIn=false 时（Anthropic）：cached 独立计价（"加项"）。
func (p Pricing) Estimate(provider, model string, u llm.Usage) float64 {
	mp, ok := p.Lookup(provider, model)
	if !ok {
		return 0
	}
	uncachedIn := u.InTokens
	if mp.CachedInIn {
		uncachedIn -= u.CachedTokens
		if uncachedIn < 0 {
			uncachedIn = 0
		}
	}
	in := float64(uncachedIn) / tokensPerMillion * mp.InputPerMUSD
	out := float64(u.OutTokens) / tokensPerMillion * mp.OutputPerMUSD
	cached := float64(u.CachedTokens) / tokensPerMillion * mp.InputPerMUSD * mp.CacheDiscount
	return in + out + cached
}
