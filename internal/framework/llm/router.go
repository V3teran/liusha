// Package llm 实现 LLM 复杂度路由。
//
// 按业务复杂度（simple / medium / complex）查询配置，
// 构造对应 Provider 并套上 retry 装饰器。
//
// 三个复杂度档位对应不同的推理能力需求：
//   - simple  → 快速响应，简单任务（如信息提取、格式化）
//   - medium  → 标准推理，常规任务（如漏洞检测、工具调用）
//   - complex → 深度推理，复杂决策（如战略规划、多步分析）
package llm

import (
	"context"
	"fmt"
	"sync"
)

// Complexity 是 LLM 复杂度分级标识。
type Complexity string

// ComplexityMedium 等枚举定义。
const (
	// ComplexitySimple 等枚举复杂度档位：Runner 按 action 复杂度选档，配置决定各档命中的 provider。
	ComplexitySimple  Complexity = "simple"  // 快速响应：信息提取、格式化、简单验证
	ComplexityMedium  Complexity = "medium"  // 标准推理：漏洞检测、工具调用、常规分析
	ComplexityComplex Complexity = "complex" // 深度推理：战略规划、多步决策、复杂综合
)

// RouterFallbackFactory 在 primary 重试耗尽后构造兜底 Provider（可空）。
// 由调用方从配置的全局备胎（保留 role __fallback__）装配。
type RouterFallbackFactory func(ctx context.Context) (Provider, error)

// Router 按 Complexity 路由 Provider，内部缓存已构造实例（线程安全）。
type Router struct {
	store     RouterStore
	pool      *ClientPool
	fallback  RouterFallbackFactory
	// providerWrapper 可选装饰钩子：build() 产出 Provider 后包一层（埋点/限流等）。
	// 在首次 For() 之前设置；并发不安全。
	providerWrapper func(Provider) Provider
	mu              sync.Mutex
	cached          map[Complexity]Provider
}

// NewRouter 构造 Router。
func NewRouter(store RouterStore) *Router {
	return NewRouterWithFallback(store, nil)
}

// NewRouterWithFallback 构造带可选兜底的 Router。
func NewRouterWithFallback(store RouterStore, fallback RouterFallbackFactory) *Router {
	return &Router{
		store:    store,
		pool:     NewClientPool(),
		fallback: fallback,
		cached:   make(map[Complexity]Provider),
	}
}

// For 返回指定 Complexity 的 Provider（首次构造后缓存）。
// 配置变更后可调用 Invalidate 清缓存。
func (r *Router) For(ctx context.Context, complexity Complexity) (Provider, error) {
	r.mu.Lock()
	if p, ok := r.cached[complexity]; ok {
		r.mu.Unlock()
		return p, nil
	}
	r.mu.Unlock()

	p, err := r.build(ctx, complexity)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.cached[complexity] = p
	r.mu.Unlock()
	return p, nil
}

// Invalidate 清除指定 complexity 的缓存（配置热更新时调用）。
func (r *Router) Invalidate(complexity Complexity) {
	r.mu.Lock()
	delete(r.cached, complexity)
	r.mu.Unlock()
}

// InvalidateAll 清除所有缓存。
func (r *Router) InvalidateAll() {
	r.mu.Lock()
	r.cached = make(map[Complexity]Provider)
	r.mu.Unlock()
}

func (r *Router) build(ctx context.Context, complexity Complexity) (Provider, error) {
	routing, err := r.store.GetRouting(ctx)
	if err != nil {
		return nil, fmt.Errorf("provider/router: load routing: %w", err)
	}
	providerKey, ok := routing.Roles[string(complexity)]
	if !ok {
		return nil, fmt.Errorf("provider/router: no provider configured for complexity %q", complexity)
	}

	spec, err := r.store.GetProviderSpec(ctx, providerKey)
	if err != nil {
		return nil, fmt.Errorf("provider/router: load provider %q: %w", providerKey, err)
	}

	g, err := BuildGeneratorWithKey(ctx, spec, r.pool)
	if err != nil {
		return nil, err
	}

	// fallback（可选）：由 store 提供（保留 role __fallback__），primary 耗尽时兜底。
	var fallback Provider
	if r.fallback != nil {
		if fb, fbErr := r.fallback(ctx); fbErr == nil && fb != nil {
			fallback = fb
		}
	}

	prov := WithFallback(NewProvider(g), fallback, DefaultRetryOptions())
	if r.providerWrapper != nil {
		prov = r.providerWrapper(prov)
	}
	return prov, nil
}

// SetProviderWrapper 注入 Provider 装饰钩子（埋点/限流等），在首次 For() 之前调用。
// 包裹点在 retry/fallback 之外——记录"调用方视角"的调用。
func (r *Router) SetProviderWrapper(fn func(Provider) Provider) {
	r.mu.Lock()
	r.providerWrapper = fn
	r.cached = make(map[Complexity]Provider) // 换钩子后作废旧缓存
	r.mu.Unlock()
}

// BuildGeneratorWithKey 用 ClientPool 共享 HTTP client 构造 Generator（按 provider 类型分派）。
// spec.APIKey 必须是已解析的明文密钥（解析归业务侧）。导出供业务策略层与 router 共用。
func BuildGeneratorWithKey(ctx context.Context, p ProviderSpec, pool *ClientPool) (Generator, error) {
	switch p.Type {
	case ProviderTypeAnthropic:
		cli, err := pool.GetOrCreateAnthropic(p.BaseURL, p.APIKey)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Key, err)
		}
		return NewAnthropic(ctx, p.Key, AnthropicConfig{
			BaseURL: p.BaseURL, Model: p.Model, APIKey: p.APIKey, MaxTokens: p.MaxTokens,
		}, cli)
	case ProviderTypeOpenAICompat, "":
		cli, err := pool.GetOrCreateOpenAI(p.BaseURL, p.APIKey)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Key, err)
		}
		return NewOpenAICompat(ctx, p.Key, OpenAICompatConfig{
			BaseURL: p.BaseURL, Model: p.Model, APIKey: p.APIKey, MaxTokens: p.MaxTokens,
			SupportsVision: p.SupportsVision,
		}, cli)
	}
	return nil, fmt.Errorf("provider %q 类型 %q 未知（支持: openai_compat / anthropic）", p.Key, p.Type)
}
