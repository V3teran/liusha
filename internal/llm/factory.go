// Package llm 的 Factory 实现：role → provider 部署 → 无状态 Generator 路由。
//
// 关键不变量：
//   - Generator 不缓存（避免跨 task tools 错乱），底层 HTTP client 由 ClientPool 共享。
//   - 路由不再读静态 cfg：role→provider 的解析由 Resolver（*llmstore.Store）在**运行期**
//     经多级缓存完成，前端改「模型」模块即时生效（取代旧的 cfg.LLM.Agents + lookupLLMField switch）。
//
// 路由规则（全在 Resolver 内，见 internal/llmstore；0105 引入 tier 中间层后为两跳）：
//   - role → AgentTier(role) 归档（heavy/vision/light，固定在代码）→ 该档命中的 provider key；
//     档未绑定则回落隐式默认档 heavy
//   - provider key → llm_provider 部署行
//   - 解析不出（档及 heavy 均未绑定）→ Resolver 返回 *llmstore.UnresolvedError，For 透传
package llm

import (
	"context"
	"fmt"

	"github.com/V3teran/liusha/internal/config/llmcfg"
)

// Provider 类型常量；与 llm_provider.type 一致。
const (
	// ProviderTypeOpenAICompat 走 OpenAI 协议族（OpenAI/DeepSeek/Qwen/Moonshot/Together/Groq/智谱/豆包/Yi/...）。
	ProviderTypeOpenAICompat = "openai_compat"
	// ProviderTypeAnthropic 走 Anthropic 原生 /v1/messages 协议。
	ProviderTypeAnthropic = "anthropic"
)

// Resolver 把 role 解析到 provider 部署行，并能取全局备胎（*llmstore.Store 自动满足）。
// 解析走多级缓存（L1/L2/DB），运行期热读，故前端改配置即时对全进程生效。
type Resolver interface {
	ProviderForRole(ctx context.Context, role string) (llmcfg.Provider, error)
	ProviderForFallback(ctx context.Context) (llmcfg.Provider, error)
}

// Builder 抽象 provider 构造逻辑，便于测试注入 mock。
//
// 第 2 参 pool 让 Builder 能复用 ClientPool 内的共享 client；
// 测试 mock 可忽略 pool 直接返回 stub Generator。
type Builder func(ctx context.Context, p llmcfg.Provider, pool *ClientPool) (Generator, error)

// Factory 按 role 路由出无状态 Generator（每 task 新建，共享底层 HTTP client）。
type Factory struct {
	resolver Resolver
	pool     *ClientPool
	builder  Builder
}

// NewFactory 用默认 BuildProvider 构造一个 Factory；dec 解密 provider 的加密密钥
// （*cryptx.Cipher 满足 llmcfg.KeyDecrypter），仅在此处（即将构造 client 前）解密，
// 解密结果不进任何缓存层。
func NewFactory(resolver Resolver, dec llmcfg.KeyDecrypter) *Factory {
	return NewFactoryWithBuilder(resolver, newBuildProvider(dec))
}

// NewFactoryWithBuilder 用自定义 builder 构造，便于测试。
func NewFactoryWithBuilder(resolver Resolver, builder Builder) *Factory {
	return &Factory{resolver: resolver, pool: NewClientPool(), builder: builder}
}

// For 按 role 解析 provider 部署并返回 Generator（每次新建无状态实例）。
//
// 重要：tools 不在此处绑定，调用方在 Generate(ctx, msgs, tools) 时传入。
func (f *Factory) For(ctx context.Context, role string) (Generator, error) {
	p, err := f.resolver.ProviderForRole(ctx, role)
	if err != nil {
		return nil, fmt.Errorf("llm.For(%q): 解析 provider: %w", role, err)
	}
	g, err := f.builder(ctx, p, f.pool)
	if err != nil {
		return nil, fmt.Errorf("llm.For(%q): build provider %q: %w", role, p.Key, err)
	}
	return g, nil
}

// forFallback 解析全局备胎 provider 部署并返回 Generator（绕开 role 路由）。
// 给 Router 构造 fallback 用：备胎是保留 role __fallback__，不属于任何业务 role。未配置时透传解析错误。
func (f *Factory) forFallback(ctx context.Context) (Generator, error) {
	p, err := f.resolver.ProviderForFallback(ctx)
	if err != nil {
		return nil, fmt.Errorf("llm.forFallback: 解析 provider: %w", err)
	}
	g, err := f.builder(ctx, p, f.pool)
	if err != nil {
		return nil, fmt.Errorf("llm.forFallback: build provider %q: %w", p.Key, err)
	}
	return g, nil
}

// newBuildProvider 用给定的解密器构造一个 Builder：解密只发生在这一刻（即将构造 client 前），
// 解密结果（明文 key）不返回给调用方、不落任何结构体字段、不进缓存——用完即弃。
func newBuildProvider(dec llmcfg.KeyDecrypter) Builder {
	return func(ctx context.Context, p llmcfg.Provider, pool *ClientPool) (Generator, error) {
		return buildProvider(ctx, p, pool, dec)
	}
}

// BuildProvider 是测试/无加密场景的默认 Builder：解密器为空时，ResolveAPIKey 回退
// os.Getenv(APIKeyEnv)（旧数据路径），EncryptedAPIKey 非空但无解密器会报错。
func BuildProvider(ctx context.Context, p llmcfg.Provider, pool *ClientPool) (Generator, error) {
	return buildProvider(ctx, p, pool, nil)
}

// buildProvider 用 ClientPool 共享 HTTP client，构造无状态 Generator。
//
// 按 Provider.Type 路由到 OpenAI 兼容（sashabaranov/go-openai）或 Anthropic 原生 SDK。
// dec 为 nil 时 ResolveAPIKey 只能走 APIKeyEnv 回退路径（EncryptedAPIKey 非空会报错）。
func buildProvider(ctx context.Context, p llmcfg.Provider, pool *ClientPool, dec llmcfg.KeyDecrypter) (Generator, error) {
	apiKey, err := llmcfg.ResolveAPIKey(p, dec)
	if err != nil {
		return nil, err
	}
	return BuildProviderWithKey(ctx, p, pool, apiKey)
}

// BuildProviderWithKey 用已解析好的明文 key 构造 Generator，跳过 ResolveAPIKey 的密钥来源解析。
//
// 供实连探测（测试连接）复用：前端可直填一把尚未落库的明文 key（新建/更换密钥场景），
// 此时密钥不来自 EncryptedAPIKey 也不来自 env，直接注入。常规路径经 buildProvider → 本函数，
// 二者共享同一 openai/anthropic client 构造分支（DRY），探测与运行期行为一致。
func BuildProviderWithKey(ctx context.Context, p llmcfg.Provider, pool *ClientPool, apiKey string) (Generator, error) {
	switch p.Type {
	case ProviderTypeOpenAICompat, "":
		// 默认（type 为空）按 OpenAI 兼容协议。
		cli, err := pool.GetOrCreateOpenAI(p.BaseURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Key, err)
		}
		return NewOpenAICompat(ctx, p.Key, OpenAICompatConfig{
			BaseURL: p.BaseURL, Model: p.DefaultModel, APIKey: apiKey, MaxTokens: p.MaxTokens,
			SupportsVision: p.SupportsVision,
		}, cli)
	case ProviderTypeAnthropic:
		cli, err := pool.GetOrCreateAnthropic(p.BaseURL, apiKey)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Key, err)
		}
		return NewAnthropic(ctx, p.Key, AnthropicConfig{
			BaseURL: p.BaseURL, Model: p.DefaultModel, APIKey: apiKey, MaxTokens: p.MaxTokens,
		}, cli)
	}
	return nil, fmt.Errorf("provider %q 类型 %q 未知（支持: openai_compat / anthropic）", p.Key, p.Type)
}
