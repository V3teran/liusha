// Package grounding 的 model + vendor 双层 registry — vision provider 坐标系自动推断。
//
// 背景：液砂全球用户群跨 Qwen-VL 派（Doubao/通义/GLM/Gemini）+ Claude/GPT 派两类 grounding
// 训练范式。要求用户配 yaml 时填 grounding_coord_system 是隐性专业知识——本 registry 自动推断。
//
// 4 层级联推断（config.Load 启动期跑）：
//   Layer 1: cfg.GroundingCoordSystem 显式填了 → 用它（escape hatch）
//   Layer 2: GuessByModelName(default_model)   → 命中精确（细粒度，模型 prefix）
//   Layer 3: GuessByBaseURL(base_url)          → vendor 兜底（豆包 endpoint ID 必须）
//   Layer 4: real_pixels + log WARN             → 兜底防启动失败
//
// 设计原则：
//   - registry 只放**有据可查**的模型/vendor，不瞎猜
//   - model name 用最长前缀匹配（避免 "claude-3" 抢 "claude-3-5"）
//   - vendor 用 base_url 子串包含匹配（容忍尾斜杠 / 版本路径差异）
package grounding

import (
	"sort"
	"strings"
)

// modelCoordSystemRegistry 是模型 prefix → CoordSystem 映射。
// key 用小写 prefix（GuessByModelName 内 ToLower 入参对齐）。
// 长 prefix 优先 — 见 GuessByModelName 内排序。
var modelCoordSystemRegistry = map[string]CoordSystem{
	// === Qwen-VL 派（normalized_1000，论文/实测确认）===
	"qwen-vl":    Normalized1000,
	"qwen2-vl":   Normalized1000,
	"qwen2.5-vl": Normalized1000,
	"qwen3-vl":   Normalized1000,

	// === 字节豆包 Seed（继承 Qwen-VL 范式，spike test 实测确认 ~3px 精度）===
	"doubao-seed":   Normalized1000,
	"doubao-1-5":    Normalized1000,
	"doubao-pro":    Normalized1000,
	"doubao-vision": Normalized1000,

	// === 智谱 GLM（继承 Qwen 派）===
	"glm-4v":   Normalized1000,
	"glm-4.5v": Normalized1000,
	"glm-4.6v": Normalized1000,

	// === Google Gemini（normalized_1000）===
	"gemini-1.5":   Normalized1000,
	"gemini-2":     Normalized1000,
	"gemini-pro":   Normalized1000,
	"gemini-flash": Normalized1000,
	"gemini-ultra": Normalized1000,

	// === 国产开源同 Qwen 派 ===
	"intern-vl": Normalized1000,
	"internvl":  Normalized1000,
	"minicpm-v": Normalized1000,
	"yi-vl":     Normalized1000,
	"cogvlm":    Normalized1000,
	"cogagent":  Normalized1000, // 智谱 grounding 专版

	// === Anthropic Claude（real_pixels，computer-use 标准）===
	"claude-3":      RealPixels,
	"claude-3-5":    RealPixels,
	"claude-3-7":    RealPixels,
	"claude-4":      RealPixels,
	"claude-sonnet": RealPixels,
	"claude-haiku":  RealPixels,
	"claude-opus":   RealPixels,

	// === OpenAI（real_pixels）===
	"gpt-4o":       RealPixels,
	"gpt-4-vision": RealPixels,
	"gpt-4-turbo":  RealPixels,
	"gpt-5":        RealPixels,
	"o1":           RealPixels,
	"o3":           RealPixels,

	// === Meta Llama Vision（real_pixels）===
	"llama-3-vision":   RealPixels,
	"llama-3.2-vision": RealPixels,
	"llama-4-vision":   RealPixels,

	// === Microsoft Phi Vision（real_pixels）===
	"phi-3-vision": RealPixels,
	"phi-4-vision": RealPixels,
}

// vendorCoordSystemRegistry 是 base_url 子串 → CoordSystem 映射。
// 一个 vendor 通常坚持一种 grounding 训练范式——本表覆盖主流 vendor。
// 用 strings.Contains 匹配 base_url（兼容 https://x.com/v1, https://x.com/api/v3 等路径变化）。
var vendorCoordSystemRegistry = map[string]CoordSystem{
	// Qwen-VL 派 vendor
	"ark.cn-beijing.volces.com":         Normalized1000, // 字节火山方舟（豆包 endpoint ID 主要场景）
	"dashscope.aliyuncs.com":            Normalized1000, // 阿里灵积 Qwen
	"open.bigmodel.cn":                  Normalized1000, // 智谱 GLM
	"generativelanguage.googleapis.com": Normalized1000, // Google Gemini
	"api.minimax.chat":                  Normalized1000, // MiniMax
	"platform.kimi.com":                 Normalized1000, // Moonshot Kimi
	"spark-api-open.xf-yun.com":         Normalized1000, // 讯飞星火

	// real_pixels 派 vendor
	"api.anthropic.com": RealPixels,
	"api.openai.com":    RealPixels,
	"api.x.ai":          RealPixels, // xAI Grok（Anthropic 范式）
}

// GuessByModelName 按 model name 前缀匹配 registry，返 (CoordSystem, hit)。
// 最长前缀优先——避免 "claude-3" 抢 "claude-3-5-sonnet"。
// 入参大小写不敏感（registry 全用小写存）。
func GuessByModelName(modelName string) (CoordSystem, bool) {
	if modelName == "" {
		return "", false
	}
	lower := strings.ToLower(modelName)
	// 排序 registry key 按长度降序——保证最长前缀优先命中。
	keys := make([]string, 0, len(modelCoordSystemRegistry))
	for k := range modelCoordSystemRegistry {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	for _, prefix := range keys {
		if strings.HasPrefix(lower, prefix) {
			return modelCoordSystemRegistry[prefix], true
		}
	}
	return "", false
}

// GuessByBaseURL 按 base_url 子串匹配 vendor registry，返 (CoordSystem, hit)。
// 用 strings.Contains 而非 HasPrefix——兼容协议（http/https）+ 路径（/v1, /api/v3）差异。
func GuessByBaseURL(baseURL string) (CoordSystem, bool) {
	if baseURL == "" {
		return "", false
	}
	lower := strings.ToLower(baseURL)
	for vendor, sys := range vendorCoordSystemRegistry {
		if strings.Contains(lower, vendor) {
			return sys, true
		}
	}
	return "", false
}

// ResolveCoordSystem 是 4 层级联推断主入口。
//
// 入参：
//   - explicit  : ProviderConfig.GroundingCoordSystem 显式值（空字符串 = 未填）
//   - modelName : ProviderConfig.DefaultModel
//   - baseURL   : ProviderConfig.BaseURL
//
// 出参：(coordSystem, source) 其中 source ∈ {"explicit", "model", "vendor", "default"}
//
//	供 caller 决定 log 级别——"default" 走 WARN，其余 INFO。
//
// 永不返 err：即使 explicit 是非法值也走 default 兜底（防止启动失败）。validate 在别处做。
func ResolveCoordSystem(explicit, modelName, baseURL string) (CoordSystem, string) {
	// Layer 1: 显式覆盖
	if explicit != "" {
		return CoordSystem(explicit), "explicit"
	}
	// Layer 2: model name registry
	if sys, hit := GuessByModelName(modelName); hit {
		return sys, "model"
	}
	// Layer 3: vendor base_url
	if sys, hit := GuessByBaseURL(baseURL); hit {
		return sys, "vendor"
	}
	// Layer 4: real_pixels fallback
	return RealPixels, "default"
}
