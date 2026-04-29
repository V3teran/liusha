// Package observability 提供成本核算与遥测相关的工具。
//
// pricing.go 内置 spec §8.2 的 LLM 单价表，按 (provider, model, usage)
// 计算单次调用的 USD 成本。cache 折扣按 plan 给定的"加项"语义实现：
//
//	cost = in/M * input_price
//	     + out/M * output_price
//	     + cached/M * input_price * cache_discount
//
// 即 CachedTokens 视为额外计费项，复用 input 单价 × 折扣系数。
package observability

import "github.com/V3teran/liusha/internal/agent/llm"

// ModelPrice 是某个 provider/model 组合的单价定义。
// InputPerMUSD / OutputPerMUSD 单位为 USD per 1M tokens。
// CacheDiscount 为 input 缓存命中部分相对原价的折扣系数（例如 0.10 表示 10% 原价）。
type ModelPrice struct {
	InputPerMUSD  float64
	OutputPerMUSD float64
	CacheDiscount float64
}

// Pricing 是 ModelPrice 的查找表 + 成本估算实现。
type Pricing struct {
	table map[string]ModelPrice
}

// tokensPerMillion 是 1M tokens 的归一化分母，避免在公式里散落魔法数。
const tokensPerMillion = 1_000_000.0

// DefaultPricing 是 spec §8.2 内置的默认单价表。
// 包含 deepseek-chat / claude-sonnet-4-6 / claude-haiku-4-5 三个生产模型。
var DefaultPricing = Pricing{
	table: map[string]ModelPrice{
		"deepseek/deepseek-chat":      {InputPerMUSD: 0.27, OutputPerMUSD: 1.10, CacheDiscount: 0.10},
		"anthropic/claude-sonnet-4-6": {InputPerMUSD: 3.00, OutputPerMUSD: 15.00, CacheDiscount: 0.30},
		"anthropic/claude-haiku-4-5":  {InputPerMUSD: 1.00, OutputPerMUSD: 5.00, CacheDiscount: 0.30},
	},
}

// Lookup 按 (provider, model) 返回 ModelPrice；未命中返回 (zero, false)。
func (p Pricing) Lookup(provider, model string) (ModelPrice, bool) {
	mp, ok := p.table[provider+"/"+model]
	return mp, ok
}

// Estimate 计算单次调用的 USD 成本。
// 未命中单价表时返回 0（不报错，由上层决定是否记录 warning）。
func (p Pricing) Estimate(provider, model string, u llm.Usage) float64 {
	mp, ok := p.Lookup(provider, model)
	if !ok {
		return 0
	}
	in := float64(u.InTokens) / tokensPerMillion * mp.InputPerMUSD
	out := float64(u.OutTokens) / tokensPerMillion * mp.OutputPerMUSD
	cached := float64(u.CachedTokens) / tokensPerMillion * mp.InputPerMUSD * mp.CacheDiscount
	return in + out + cached
}
