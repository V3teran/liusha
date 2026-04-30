// Package llm 的 Factory 实现：role → field → provider key → 无状态 Generator 路由。
//
// v1.1（T11）改造：
//   - 不再缓存 Generator（避免 v1 跨 task tools 错乱 bug）。
//   - 通过 ClientPool 单例化底层 *openai.Client / *anthropic.Client，
//     共享 HTTP 连接池；Generator 本身无状态、每次 For 新建。
//
// 路由规则（spec §8.4 + T19 黑客松借鉴）：
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

	"github.com/V3teran/liusha/internal/config"
)

// Provider 类型常量；与 config.providers.<key>.type 一致。
const (
	// ProviderTypeOpenAICompat 走 OpenAI 协议族（OpenAI/DeepSeek/Qwen/Moonshot/Together/Groq/智谱/豆包/Yi/...）。
	ProviderTypeOpenAICompat = "openai_compat"
	// ProviderTypeAnthropic 走 Anthropic 原生 /v1/messages 协议。
	ProviderTypeAnthropic = "anthropic"
)

// Builder 抽象 provider 构造逻辑，便于测试注入 mock。
//
// 第 4 参 pool 让 Builder 能复用 ClientPool 内的共享 client；
// 测试 mock 可忽略 pool 直接返回 stub Generator。
type Builder func(ctx context.Context, cfg config.Config, providerKey string, pool *ClientPool) (Generator, error)

// Factory 按 role 路由出无状态 Generator（每 task 新建，共享底层 HTTP client）。
type Factory struct {
	cfg     config.Config
	pool    *ClientPool
	builder Builder
}

// NewFactory 用默认 BuildProvider 构造一个 Factory。
func NewFactory(cfg config.Config) *Factory {
	return NewFactoryWithBuilder(cfg, BuildProvider)
}

// NewFactoryWithBuilder 用自定义 builder 构造，便于测试。
func NewFactoryWithBuilder(cfg config.Config, builder Builder) *Factory {
	return &Factory{cfg: cfg, pool: NewClientPool(), builder: builder}
}

// For 按 role 解析 provider key 并返回 Generator（每次新建无状态实例）。
//
// 重要：tools 不在此处绑定，调用方在 Generate(ctx, msgs, tools) 时传入。
//
// 路由规则：
//  1. routes[role] = field name（如 "default_provider"）
//  2. field name → cfg.LLM 对应字段（如 cfg.LLM.DefaultProvider = "deepseek"）
//  3. 若 field 名未识别或字段值为空 → 回退 default_provider
//  4. role 不在 routes 表 → 直接走 default_provider
func (f *Factory) For(ctx context.Context, role string) (Generator, error) {
	providerKey := f.resolveProviderKey(role)
	if providerKey == "" {
		return nil, fmt.Errorf("llm.For(%q): default_provider 未配置", role)
	}
	g, err := f.builder(ctx, f.cfg, providerKey, f.pool)
	if err != nil {
		return nil, fmt.Errorf("llm.For(%q): build provider %q: %w", role, providerKey, err)
	}
	return g, nil
}

// forProviderKey 直接按 provider key 取 Generator（绕开 routes 解析）。
// 主要给 Router 构造 fallback 用：fallback_provider 字段是 provider key 而非 role。
func (f *Factory) forProviderKey(ctx context.Context, providerKey string) (Generator, error) {
	if providerKey == "" {
		return nil, fmt.Errorf("llm.forProviderKey: provider key 为空")
	}
	g, err := f.builder(ctx, f.cfg, providerKey, f.pool)
	if err != nil {
		return nil, fmt.Errorf("llm.forProviderKey(%q): %w", providerKey, err)
	}
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

// BuildProvider 用 ClientPool 共享 HTTP client，构造无状态 Generator。
//
// 按 ProviderConfig.Type 路由到 OpenAI 兼容（sashabaranov/go-openai）或 Anthropic 原生 SDK。
// APIKey 从 ProviderConfig.APIKeyEnv 指向的环境变量取，为空报错。
func BuildProvider(ctx context.Context, cfg config.Config, providerKey string, pool *ClientPool) (Generator, error) {
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
		cli, err := pool.GetOrCreateOpenAI(pc.BaseURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", providerKey, err)
		}
		return NewOpenAICompat(ctx, providerKey, OpenAICompatConfig{
			BaseURL: pc.BaseURL, Model: pc.DefaultModel, APIKey: apiKey, MaxTokens: pc.MaxTokens,
		}, cli)
	case ProviderTypeAnthropic:
		cli, err := pool.GetOrCreateAnthropic(pc.BaseURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", providerKey, err)
		}
		return NewAnthropic(ctx, providerKey, AnthropicConfig{
			BaseURL: pc.BaseURL, Model: pc.DefaultModel, APIKey: apiKey, MaxTokens: pc.MaxTokens,
		}, cli)
	}
	return nil, fmt.Errorf("provider %q 类型 %q 未知（支持: openai_compat / anthropic）", providerKey, pc.Type)
}
