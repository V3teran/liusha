package observability

import (
	"math"
	"testing"

	"github.com/V3teran/liusha/internal/llm"
)

// TestEstimate_DeepSeek 验证 deepseek-chat 的基础单价计算
// 1M in + 1M out = 0.27 + 1.10 = 1.37 USD
func TestEstimate_DeepSeek(t *testing.T) {
	got := DefaultPricing.Estimate("deepseek", "deepseek-chat", llm.Usage{
		InTokens:  1_000_000,
		OutTokens: 1_000_000,
	})
	want := 0.27 + 1.10
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestEstimate_Anthropic_CacheDiscount 验证 cache 折扣（"加项"语义）
// Anthropic CachedInIn=false：cache_read_input_tokens 与 input_tokens 独立返回。
// 1M in 走全价 3.00；额外 1M cached 走 3.00 * 0.30 = 0.90
// 总计 3.00 + 0.90 = 3.90
func TestEstimate_Anthropic_CacheDiscount(t *testing.T) {
	got := DefaultPricing.Estimate("anthropic", "claude-sonnet-4-6", llm.Usage{
		InTokens:     1_000_000,
		CachedTokens: 1_000_000,
	})
	want := 3.9
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestEstimate_DeepSeek_CachedSubtracts 验证 DeepSeek "子集"语义。
// CachedInIn=true：CachedTokens ⊆ InTokens，应先减再分别计价。
//   - 未缓存 in: (1M - 800K)/M * 0.27       = 0.054
//   - cached  : 800K/M * 0.27 * 0.10        = 0.0216
//   - out     : 100K/M * 1.10               = 0.110
//   - total                                  = 0.1856
//
// 旧公式（错误"加项"）会算成 1M*0.27 + 100K/M*1.10 + 800K*0.27*0.10 = 0.4016。
func TestEstimate_DeepSeek_CachedSubtracts(t *testing.T) {
	got := DefaultPricing.Estimate("deepseek", "deepseek-chat", llm.Usage{
		InTokens:     1_000_000,
		OutTokens:    100_000,
		CachedTokens: 800_000,
	})
	want := 0.054 + 0.0216 + 0.110
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestEstimate_DeepSeek_CachedExceedsIn_ClampsAtZero 防御性测试：
// 异常情况下 cached > in 时 uncachedIn 应被 clamp 至 0，不出现负价。
func TestEstimate_DeepSeek_CachedExceedsIn_ClampsAtZero(t *testing.T) {
	got := DefaultPricing.Estimate("deepseek", "deepseek-chat", llm.Usage{
		InTokens:     1_000_000,
		CachedTokens: 2_000_000, // 异常输入
	})
	// uncachedIn 0 + cached 2M*0.27*0.10 = 0.054
	want := 0.054
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestEstimate_Haiku 验证 haiku 单价
// 1M in + 1M out = 1.00 + 5.00 = 6.00 USD
func TestEstimate_Haiku(t *testing.T) {
	got := DefaultPricing.Estimate("anthropic", "claude-haiku-4-5", llm.Usage{
		InTokens:  1_000_000,
		OutTokens: 1_000_000,
	})
	want := 1.00 + 5.00
	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestEstimate_UnknownReturnsZero 未知 provider/model 返回 0
func TestEstimate_UnknownReturnsZero(t *testing.T) {
	if v := DefaultPricing.Estimate("unknown", "x", llm.Usage{InTokens: 100}); v != 0 {
		t.Fatalf("expected 0 for unknown provider, got %v", v)
	}
}

// TestEstimate_ZeroUsage 零用量返回 0
func TestEstimate_ZeroUsage(t *testing.T) {
	if v := DefaultPricing.Estimate("deepseek", "deepseek-chat", llm.Usage{}); v != 0 {
		t.Fatalf("expected 0 for zero usage, got %v", v)
	}
}

// TestEstimate_SmallUsage 小量 token 计算（精度验证）
// 1000 in + 500 out, deepseek-chat
// = 1000/1M * 0.27 + 500/1M * 1.10 = 0.00027 + 0.00055 = 0.00082
func TestEstimate_SmallUsage(t *testing.T) {
	got := DefaultPricing.Estimate("deepseek", "deepseek-chat", llm.Usage{
		InTokens:  1000,
		OutTokens: 500,
	})
	want := 0.00027 + 0.00055
	if math.Abs(got-want) > 1e-9 {
		t.Fatalf("got %v want %v", got, want)
	}
}
