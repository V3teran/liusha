// Package llm 的 Factory 实现：role → field → provider key → Generator 路由 + 懒加载缓存。
//
// 黑客松借鉴创新 11（共识 E）：
//   - 通过 cfg.LLM.Routes 表把抽象角色（"react_main"/"observer"/"distill"…）解耦到具体 provider，
//     允许同一 role 在不同部署里换底层模型而不改代码。
//   - field 名（"default_provider"/"light_provider"/"vision_provider"/"fallback_provider"）是
//     LLMConfig 的 4 个字段抽象，通过 switch 解到当前 provider key。
//   - 未在 routes 列表的 role 一律回退 default_provider，保证 runtime 永远拿得到 Generator。
package llm

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/V3teran/liusha/internal/config"
)

// Builder 抽象 provider 构造逻辑，便于测试注入 mock。
type Builder func(ctx context.Context, cfg config.Config, providerKey string, tools []ToolSchema) (Generator, error)

// Factory 按 role 路由 + 懒加载缓存 Generator。
//
// 不直接持有 LLM client；按需通过 builder 构造，按 provider key 缓存。
// 同一 provider key 在多个 role 之间共享同一 Generator 实例（共享内部 ChatModel）。
type Factory struct {
	cfg     config.Config
	builder Builder

	mu    sync.Mutex
	cache map[string]Generator // provider key → Generator
}

// NewFactory 用默认 BuildProvider 构造一个 Factory。
func NewFactory(cfg config.Config) *Factory {
	return NewFactoryWithBuilder(cfg, BuildProvider)
}

// NewFactoryWithBuilder 用自定义 builder 构造，便于测试。
func NewFactoryWithBuilder(cfg config.Config, builder Builder) *Factory {
	return &Factory{
		cfg:     cfg,
		builder: builder,
		cache:   make(map[string]Generator),
	}
}

// For 按 role 解析 provider key 并返回缓存的 Generator（首次调用懒构造）。
//
// 路由规则（spec §8.4 + T19 黑客松借鉴）：
//  1. routes[role] = field name（如 "default_provider"）
//  2. field name → cfg.LLM 对应字段（如 cfg.LLM.DefaultProvider = "deepseek"）
//  3. 若 field 名未识别或字段值为空 → 回退 default_provider
//  4. role 不在 routes 表 → 直接走 default_provider
//  5. 按 provider key 缓存（同 key 多 role 共享）
func (f *Factory) For(ctx context.Context, role string, tools []ToolSchema) (Generator, error) {
	providerKey := f.resolveProviderKey(role)
	if providerKey == "" {
		return nil, fmt.Errorf("llm.For(%q): default_provider 未配置", role)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if g, ok := f.cache[providerKey]; ok {
		return g, nil
	}
	g, err := f.builder(ctx, f.cfg, providerKey, tools)
	if err != nil {
		return nil, fmt.Errorf("llm.For(%q): build provider %q: %w", role, providerKey, err)
	}
	f.cache[providerKey] = g
	return g, nil
}

// forProviderKey 直接按 provider key 取/构造 Generator（绕开 routes 解析）。
// 主要给 Router 构造 fallback 用：fallback_provider 字段是 provider key 而非 role。
func (f *Factory) forProviderKey(ctx context.Context, providerKey string, tools []ToolSchema) (Generator, error) {
	if providerKey == "" {
		return nil, fmt.Errorf("llm.forProviderKey: provider key 为空")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if g, ok := f.cache[providerKey]; ok {
		return g, nil
	}
	g, err := f.builder(ctx, f.cfg, providerKey, tools)
	if err != nil {
		return nil, fmt.Errorf("llm.forProviderKey(%q): %w", providerKey, err)
	}
	f.cache[providerKey] = g
	return g, nil
}

// resolveProviderKey 把 role 解析到具体 provider key（"deepseek"/"anthropic"/...）。
// 任何无法解析的中间步骤都回退 default_provider。
func (f *Factory) resolveProviderKey(role string) string {
	field, ok := f.cfg.LLM.Routes[role]
	if !ok {
		return f.cfg.LLM.DefaultProvider
	}
	if key := lookupLLMField(f.cfg.LLM, field); key != "" {
		return key
	}
	return f.cfg.LLM.DefaultProvider
}

// lookupLLMField 把 LLMConfig 的 4 个 field 名映射到对应字符串值。
// 用 switch 而非反射，避免反射开销 + 拼写错误更早暴露。
func lookupLLMField(c config.LLMConfig, field string) string {
	switch field {
	case "default_provider":
		return c.DefaultProvider
	case "light_provider":
		return c.LightProvider
	case "vision_provider":
		return c.VisionProvider
	case "fallback_provider":
		return c.FallbackProvider
	}
	return ""
}

// Provider 类型常量；与 config.providers.<key>.type 一致。
const (
	// ProviderTypeOpenAICompat 走 OpenAI 协议族（OpenAI/DeepSeek/Qwen/Moonshot/Together/Groq/智谱/豆包/Yi/...）。
	ProviderTypeOpenAICompat = "openai_compat"
	// ProviderTypeAnthropic 走 Anthropic 原生 /v1/messages 协议。
	ProviderTypeAnthropic = "anthropic"
)

// BuildProvider 根据 cfg.Providers[providerKey] 构造 Generator。
//
// 按 ProviderConfig.Type 路由到 OpenAI 兼容（sashabaranov/go-openai）或 Anthropic 原生 SDK。
// tools 一次性绑定（每个 task/调用上下文独立）。
// APIKey 从 ProviderConfig.APIKeyEnv 指向的环境变量取，为空报错。
func BuildProvider(ctx context.Context, cfg config.Config, providerKey string, tools []ToolSchema) (Generator, error) {
	pc, ok := cfg.Providers[providerKey]
	if !ok {
		return nil, fmt.Errorf("provider %q 未在 config.providers 中配置", providerKey)
	}
	apiKey := os.Getenv(pc.APIKeyEnv)
	if apiKey == "" {
		return nil, fmt.Errorf("env %s 为空（provider=%s）", pc.APIKeyEnv, providerKey)
	}
	switch pc.Type {
	case ProviderTypeOpenAICompat, "":
		// 默认（type 为空）按 OpenAI 兼容协议；老配置无 type 字段时也能跑。
		return NewOpenAICompat(ctx, providerKey, OpenAICompatConfig{
			BaseURL: pc.BaseURL, Model: pc.DefaultModel, APIKey: apiKey, MaxTokens: pc.MaxTokens,
		}, tools)
	case ProviderTypeAnthropic:
		return NewAnthropic(ctx, providerKey, AnthropicConfig{
			BaseURL: pc.BaseURL, Model: pc.DefaultModel, APIKey: apiKey, MaxTokens: pc.MaxTokens,
		}, tools)
	}
	return nil, fmt.Errorf("provider %q 类型 %q 未知（支持: openai_compat / anthropic）", providerKey, pc.Type)
}
