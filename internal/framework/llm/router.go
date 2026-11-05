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
	"time"
)

// Complexity 是 LLM 复杂度分级标识。
type Complexity string

// ComplexityMedium 等枚举定义。
const (
	// ComplexitySimple 等枚举复杂度档位：按 agent.complexity（文档 complexity 种子）选档，配置决定各档命中的 provider。
	ComplexitySimple  Complexity = "simple"  // 快速响应：信息提取、格式化、简单验证
	ComplexityMedium  Complexity = "medium"  // 标准推理：漏洞检测、工具调用、常规分析
	ComplexityComplex Complexity = "complex" // 深度推理：战略规划、多步决策、复杂综合
)

// ParseComplexity 把 agent.complexity（文档 complexity 种子经三级缓存读）收敛成合法档位；
// 缺失/非法值回退 medium——全仓唯一兜底点，调用方不得再各自硬编码档位。
func ParseComplexity(complexity string) Complexity {
	switch Complexity(complexity) {
	case ComplexitySimple, ComplexityComplex:
		return Complexity(complexity)
	default:
		return ComplexityMedium
	}
}

// providerCacheTTL 是已构造 Provider 实例的缓存有效期。
// 缓存把 API key/model/baseURL 烤进实例——前端改配置后若缓存不过期，
// 新配置直到进程重启才生效。TTL 把这个窗口封顶（llmstore 侧的多级缓存
// 仍负责读路径的即时失效，本 TTL 只兜「已构造实例」这一层）。
const providerCacheTTL = 30 * time.Second

// cachedProvider 是 Router 的缓存条目（实例 + 构造时刻）。
type cachedProvider struct {
	p       Provider
	builtAt time.Time
}

// Router 按 Complexity 路由 Provider，内部缓存已构造实例（线程安全，
// 条目经 providerCacheTTL 过期，保证配置变更有界生效）。
type Router struct {
	store     RouterStore
	pool      *ClientPool
	retryOpts RetryOptions
	// providerWrapper 可选装饰钩子：build() 产出 Provider 后包一层（埋点/限流等）。
	// 在首次 For() 之前设置；并发不安全。
	providerWrapper func(Provider) Provider
	mu              sync.Mutex
	cached          map[Complexity]cachedProvider
}

// NewRouter 构造 Router（默认退避表）。
func NewRouter(store RouterStore) *Router {
	return NewRouterWithOpts(store, RetryOptions{})
}

// NewRouterWithOpts 构造 Router（opts 零值字段由 DefaultRetryOptions 逐项填充）。
func NewRouterWithOpts(store RouterStore, opts RetryOptions) *Router {
	def := DefaultRetryOptions()
	if opts.MaxRetries429 == 0 {
		opts.MaxRetries429 = def.MaxRetries429
	}
	if opts.MaxRetries529 == 0 {
		opts.MaxRetries529 = def.MaxRetries529
	}
	if opts.MaxRetries5xx == 0 {
		opts.MaxRetries5xx = def.MaxRetries5xx
	}
	if opts.MaxRetriesNet == 0 {
		opts.MaxRetriesNet = def.MaxRetriesNet
	}
	return &Router{
		store:     store,
		pool:      NewClientPool(),
		retryOpts: opts,
		cached:    make(map[Complexity]cachedProvider),
	}
}

// For 返回指定 Complexity 的 Provider（缓存命中且未过期直接返回）。
// 配置变更可调用 Invalidate 清缓存；不调用也会经 providerCacheTTL 自然过期。
func (r *Router) For(ctx context.Context, complexity Complexity) (Provider, error) {
	r.mu.Lock()
	if e, ok := r.cached[complexity]; ok && time.Since(e.builtAt) < providerCacheTTL {
		r.mu.Unlock()
		return e.p, nil
	}
	r.mu.Unlock()

	p, err := r.build(ctx, complexity)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	r.cached[complexity] = cachedProvider{p: p, builtAt: time.Now()}
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
	r.cached = make(map[Complexity]cachedProvider)
	r.mu.Unlock()
}

// routeCandidates 返回档位的解析候选序：显式档 → 就近降级（能力相邻档优先）。
// provider 少于三档时（用户只配 1-2 把 key），缺档自动落到相邻可用档：
//   complex 缺 → medium → simple；medium 缺 → complex → simple；simple 缺 → medium → complex
func routeCandidates(c Complexity) []Complexity {
	switch c {
	case ComplexityComplex:
		return []Complexity{c, ComplexityMedium, ComplexitySimple}
	case ComplexitySimple:
		return []Complexity{c, ComplexityMedium, ComplexityComplex}
	default:
		return []Complexity{c, ComplexityComplex, ComplexitySimple}
	}
}

func (r *Router) build(ctx context.Context, complexity Complexity) (Provider, error) {
	routing, err := r.store.GetRouting(ctx)
	if err != nil {
		return nil, fmt.Errorf("provider/router: load routing: %w", err)
	}

	// 按候选序解析：显式档优先；其路由缺失或 provider 不可用（被删/密钥缺失）
	// 时降级到相邻档——保证「配置 1 个 provider 也能服务全部档位」。
	var lastErr error
	for _, lvl := range routeCandidates(complexity) {
		providerKey, ok := routing.Roles[string(lvl)]
		if !ok {
			continue
		}
		spec, err := r.store.GetProviderSpec(ctx, providerKey)
		if err != nil {
			lastErr = fmt.Errorf("provider/router: load provider %q: %w", providerKey, err)
			continue
		}
		g, err := BuildGeneratorWithKey(ctx, spec, r.pool)
		if err != nil {
			lastErr = err
			continue
		}
		prov := WithRetry(NewProvider(g), r.retryOpts)
		if r.providerWrapper != nil {
			prov = r.providerWrapper(prov)
		}
		return prov, nil
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf(
		"provider/router: no LLM provider configured for complexity %q —"+
			"请在 config.yaml providers: 声明并在前端「能力分档」页设置路由", complexity)
}

// SetProviderWrapper 注入 Provider 装饰钩子（埋点/限流等），在首次 For() 之前调用。
// 包裹点在 retry/fallback 之外——记录"调用方视角"的调用。
func (r *Router) SetProviderWrapper(fn func(Provider) Provider) {
	r.mu.Lock()
	r.providerWrapper = fn
	r.cached = make(map[Complexity]cachedProvider) // 换钩子后作废旧缓存
	r.mu.Unlock()
}

// ContextWindow 返回指定档位当前路由 provider 的上下文窗口（token 数）。
// 上下文压缩预算的基准——此前 provider.context_window 配置必填却无消费者，
// 本方法让它承载真实语义。store 层已多级缓存，调用廉价；窗口未知（0）或
// 读取失败时返回错误，由调用方回退内置兜底。
func (r *Router) ContextWindow(ctx context.Context, complexity Complexity) (int, error) {
	spec, err := r.resolveSpec(ctx, complexity)
	if err != nil {
		return 0, err
	}
	if spec.ContextWindow <= 0 {
		return 0, fmt.Errorf("provider/router: provider %q context_window 未配置", spec.Code)
	}
	return spec.ContextWindow, nil
}

// resolveSpec 按候选序解析档位对应的 provider spec（显式档 → 就近降级）。
func (r *Router) resolveSpec(ctx context.Context, complexity Complexity) (ProviderSpec, error) {
	routing, err := r.store.GetRouting(ctx)
	if err != nil {
		return ProviderSpec{}, fmt.Errorf("provider/router: load routing: %w", err)
	}
	var lastErr error
	for _, lvl := range routeCandidates(complexity) {
		providerKey, ok := routing.Roles[string(lvl)]
		if !ok {
			continue
		}
		spec, err := r.store.GetProviderSpec(ctx, providerKey)
		if err != nil {
			lastErr = fmt.Errorf("provider/router: load provider %q: %w", providerKey, err)
			continue
		}
		return spec, nil
	}
	if lastErr != nil {
		return ProviderSpec{}, lastErr
	}
	return ProviderSpec{}, fmt.Errorf(
		"provider/router: no LLM provider configured for complexity %q —"+
			"请在 config.yaml providers: 声明并在前端「能力分档」页设置路由", complexity)
}

// BuildGeneratorWithKey 用 ClientPool 共享 HTTP client 构造 Generator（按 provider 类型分派）。
// spec.APIKey 必须是已解析的明文密钥（解析归业务侧）。导出供业务策略层与 router 共用。
func BuildGeneratorWithKey(ctx context.Context, p ProviderSpec, pool *ClientPool) (Generator, error) {
	switch p.Type {
	case ProviderTypeAnthropic:
		cli, err := pool.GetOrCreateAnthropic(p.BaseURL, p.APIKey)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Code, err)
		}
		return NewAnthropic(ctx, p.Code, AnthropicConfig{
			BaseURL: p.BaseURL, Model: p.Model, MaxTokens: p.MaxTokens,
			ThinkingBudget: p.ThinkingBudget,
		}, cli)
	case ProviderTypeOpenAICompat, "":
		cli, err := pool.GetOrCreateOpenAI(p.BaseURL, p.APIKey)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Code, err)
		}
		return NewOpenAICompat(ctx, p.Code, OpenAICompatConfig{
			BaseURL: p.BaseURL, Model: p.Model, MaxTokens: p.MaxTokens,
			SupportsVision: p.SupportsVision,
		}, cli)
	}
	return nil, fmt.Errorf("provider %q 类型 %q 未知（支持: openai_compat / anthropic）", p.Code, p.Type)
}
