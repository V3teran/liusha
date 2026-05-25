package grounding

import "testing"

func TestGuessByModelName_LongestPrefix(t *testing.T) {
	// 验证最长前缀匹配——"claude-3-5-sonnet" 应命中 "claude-3-5" 不是 "claude-3"
	cases := []struct {
		model string
		want  CoordSystem
		hit   bool
	}{
		{"claude-3-5-sonnet-20241022", RealPixels, true},      // 命中 "claude-3-5"
		{"claude-3-haiku-20240307", RealPixels, true},         // 命中 "claude-3"
		{"claude-4-sonnet", RealPixels, true},                 // 命中 "claude-4"
		{"qwen3-vl-32b-instruct", Normalized1000, true},       // 命中 "qwen3-vl"
		{"qwen2.5-vl-7b", Normalized1000, true},               // 命中 "qwen2.5-vl"
		{"doubao-seed-2-0-mini-260428", Normalized1000, true}, // 命中 "doubao-seed"
		{"gemini-1.5-pro-002", Normalized1000, true},          // 命中 "gemini-1.5"
		{"gpt-4o-2024-11-20", RealPixels, true},               // 命中 "gpt-4o"
		{"o3-mini", RealPixels, true},                          // 命中 "o3"
		{"llama-4-vision-instruct", RealPixels, true},
		{"phi-4-vision-tiny", RealPixels, true},
	}
	for _, c := range cases {
		got, hit := GuessByModelName(c.model)
		if hit != c.hit || (hit && got != c.want) {
			t.Errorf("GuessByModelName(%q): want (%v, %v), got (%v, %v)", c.model, c.want, c.hit, got, hit)
		}
	}
}

func TestGuessByModelName_CaseInsensitive(t *testing.T) {
	if got, hit := GuessByModelName("CLAUDE-4-OPUS"); !hit || got != RealPixels {
		t.Errorf("大写应匹配，got hit=%v sys=%v", hit, got)
	}
}

func TestGuessByModelName_Miss(t *testing.T) {
	cases := []string{
		"",
		"unknown-model-x",
		"ernie-4-vl",              // 百度 — 未列入 registry
		"step-1v",                 // 阶跃 — 未列入
		"ep-20260523xxxxxx-xxxxx", // 豆包 endpoint ID — 应该 model 层 miss，走 vendor
	}
	for _, m := range cases {
		if _, hit := GuessByModelName(m); hit {
			t.Errorf("%q 不应命中 model registry", m)
		}
	}
}

func TestGuessByBaseURL_VendorMatching(t *testing.T) {
	cases := []struct {
		url  string
		want CoordSystem
		hit  bool
	}{
		// Qwen-VL 派
		{"https://ark.cn-beijing.volces.com/api/v3", Normalized1000, true},
		{"https://dashscope.aliyuncs.com/compatible-mode/v1", Normalized1000, true},
		{"https://open.bigmodel.cn/api/paas/v4", Normalized1000, true},
		{"https://generativelanguage.googleapis.com/v1beta", Normalized1000, true},
		{"https://api.minimax.chat/v1", Normalized1000, true},

		// real_pixels 派
		{"https://api.anthropic.com", RealPixels, true},
		{"https://api.openai.com/v1", RealPixels, true},
		{"https://api.x.ai/v1", RealPixels, true},

		// 未知 vendor
		{"https://api.deepseek.com/v1", "", false},
		{"https://api.unknown-vendor.com", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, hit := GuessByBaseURL(c.url)
		if hit != c.hit || (hit && got != c.want) {
			t.Errorf("GuessByBaseURL(%q): want (%v, %v), got (%v, %v)", c.url, c.want, c.hit, got, hit)
		}
	}
}

func TestResolveCoordSystem_Layer1Explicit(t *testing.T) {
	// Layer 1：显式值优先于一切
	sys, src := ResolveCoordSystem("normalized_100", "claude-4", "https://api.anthropic.com")
	if src != "explicit" || sys != "normalized_100" {
		t.Errorf("want (normalized_100, explicit), got (%v, %v)", sys, src)
	}
}

func TestResolveCoordSystem_Layer2Model(t *testing.T) {
	// Layer 2：model name registry 命中
	sys, src := ResolveCoordSystem("", "claude-3-5-sonnet", "https://proxy.example.com")
	if src != "model" || sys != RealPixels {
		t.Errorf("want (real_pixels, model), got (%v, %v)", sys, src)
	}
}

func TestResolveCoordSystem_Layer3Vendor(t *testing.T) {
	// Layer 3：endpoint ID model miss → vendor 兜底（豆包典型场景）
	sys, src := ResolveCoordSystem("", "ep-20260523xxxxxx-xxxxx", "https://ark.cn-beijing.volces.com/api/v3")
	if src != "vendor" || sys != Normalized1000 {
		t.Errorf("want (normalized_1000, vendor), got (%v, %v)", sys, src)
	}
}

func TestResolveCoordSystem_Layer4Default(t *testing.T) {
	// Layer 4：全部 miss → fallback real_pixels
	sys, src := ResolveCoordSystem("", "ernie-4-vl", "https://api.unknown.com")
	if src != "default" || sys != RealPixels {
		t.Errorf("want (real_pixels, default), got (%v, %v)", sys, src)
	}
}

func TestResolveCoordSystem_LayerOrder(t *testing.T) {
	// Layer 2 优先于 Layer 3：model registry 命中时不查 vendor
	// claude-3-5 model 命中 real_pixels，base_url 即使是 normalized_1000 vendor 也忽略
	sys, src := ResolveCoordSystem("", "claude-3-5-sonnet", "https://ark.cn-beijing.volces.com/api/v3")
	if src != "model" || sys != RealPixels {
		t.Errorf("want layer 2 win (real_pixels, model), got (%v, %v)", sys, src)
	}
}
