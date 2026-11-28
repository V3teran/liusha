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
	"strings"
	"sync"
	"time"
)

// Complexity 是 LLM 复杂度分级标识。
type Complexity string

// ComplexityMedium 等枚举定义。
const (
	// ComplexitySimple 等枚举复杂度档位：按 agent 生效配置的 complexity 选档，配置决定各档命中的 provider。
	ComplexitySimple  Complexity = "simple"  // 快速响应：信息提取、格式化、简单验证
	ComplexityMedium  Complexity = "medium"  // 标准推理：漏洞检测、工具调用、常规分析
	ComplexityComplex Complexity = "complex" // 深度推理：战略规划、多步决策、复杂综合
)

// ParseComplexity 把 agent 生效配置的 complexity 收敛成合法档位；
// 缺失/非法值回退 medium——全仓唯一兜底点，调用方不得再各自硬编码档位。
func ParseComplexity(complexity string) Complexity {
	switch Complexity(complexity) {
	case ComplexitySimple, ComplexityComplex:
		return Complexity(complexity)
	default:
		return ComplexityMedium
	}
}

// providerCacheTTL 是已构造 Provider 实例的缓存有效期，也是模型配置变更生效时间的上界。
// 实例把密钥、模型、服务地址烤在里面：读路径（llmstore）的缓存在写入后即刻失效，已构造的
// 实例不会——持有 Router 的组件在模型配置变更时调 InvalidateAll 丢掉它们；没经它作废的
// 变更（绕过写路径直接改库）由这个 TTL 兜底。
const providerCacheTTL = 30 * time.Second

// cachedProvider 是 Router 的缓存条目（实例 + 构造时刻）。
type cachedProvider struct {
	p       Provider
	builtAt time.Time
}

// Router 按 Complexity 路由 Provider，内部缓存已构造实例（线程安全）。模型配置变更时经
// InvalidateAll 作废它们，此后按新配置构造；条目另有 providerCacheTTL 的有效期。
type Router struct {
	store     RouterStore
	pool      *ClientPool
	retryOpts RetryOptions
	// governor 是账户级的节律治理（额度窗口的冷却、在途名额的上限）。额度与并发都是
	// 账户级的状态，同一账户的所有 provider、档位、调用方共用一份；挂在 Router 上是因为
	// 已构造的 Provider 实例会定期重建。
	governor *governor
	// providerWrapper 可选装饰钩子：build() 产出 Provider 后包一层（埋点/限流等）。
	// 在首次 For() 之前设置；并发不安全。
	providerWrapper func(Provider) Provider
	mu              sync.Mutex
	cached          map[Complexity]cachedProvider
	// invalidations 是缓存被整体作废的次数：一次构造若跨越了作废，它读到的可能是作废前的
	// 配置，结果不进缓存（见 For）。调用方持锁。
	invalidations uint64
}

// NewRouter 构造 Router：框架内置的退避策略，不限账户在途数。
func NewRouter(store RouterStore) *Router {
	return NewRouterWithOpts(store, DefaultRetryOptions(), 0)
}

// NewRouterWithOpts 构造 Router，按 opts 原样执行重试（次数为 0 即该类错误不重试），
// accountConcurrency 是每个账户同时在途的请求数上限（0 = 不限）。部署方的策略来自
// 配置 llm.retry 与 llm.account_concurrency，不在这里补值。
func NewRouterWithOpts(store RouterStore, opts RetryOptions, accountConcurrency int) *Router {
	return &Router{
		store:     store,
		pool:      NewClientPool(),
		retryOpts: opts,
		governor:  &governor{cooldown: newCooldown(), inflight: newInflight(accountConcurrency)},
		cached:    make(map[Complexity]cachedProvider),
	}
}

// For 返回指定 Complexity 的 Provider：缓存里有未过期的实例就用它，否则按当前配置构造。
func (r *Router) For(ctx context.Context, complexity Complexity) (Provider, error) {
	r.mu.Lock()
	if e, ok := r.cached[complexity]; ok && time.Since(e.builtAt) < providerCacheTTL {
		r.mu.Unlock()
		return e.p, nil
	}
	seen := r.invalidations
	r.mu.Unlock()

	p, err := r.build(ctx, complexity)
	if err != nil {
		return nil, err
	}

	r.mu.Lock()
	// 构造期间缓存被作废过：这次读到的可能是作废前的配置，结果不进缓存——进了就要再活一个
	// TTL，这次变更对该档位就不是立即生效。这一次调用照用它的结果：它开始于作废之前，
	// 与已经发出去的请求同样不受影响。
	if r.invalidations == seen {
		r.cached[complexity] = cachedProvider{p: p, builtAt: time.Now()}
	}
	r.mu.Unlock()
	return p, nil
}

// InvalidateAll 作废全部已构造的实例，下一次 For 按当前配置重新构造。模型配置（provider、
// 角色路由）变更后调用。已经发出去的请求不受影响——它们用的是作废前构造的实例。
func (r *Router) InvalidateAll() {
	r.mu.Lock()
	r.dropCached()
	r.mu.Unlock()
}

// dropCached 作废全部已构造的实例。调用方持锁。
func (r *Router) dropCached() {
	r.cached = make(map[Complexity]cachedProvider)
	r.invalidations++
}

// routeCandidates 返回档位的解析候选序：显式档 → 就近降级（能力相邻档优先）。
// provider 少于三档时（用户只配 1-2 把 key），缺档自动落到相邻可用档：
//
//	complex 缺 → medium → simple；medium 缺 → complex → simple；simple 缺 → medium → complex
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
	var g Generator
	var account string
	var outputLimit int
	_, err := r.resolve(ctx, complexity, func(spec ProviderSpec) error {
		built, err := BuildGeneratorWithKey(ctx, spec, r.pool)
		if err == nil {
			g, account, outputLimit = built, quotaIdentity(spec.BaseURL, spec.APIKey), spec.MaxTokens
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	prov := WithRetry(NewProvider(g), r.retryOpts, r.governor, account)
	if r.providerWrapper != nil {
		prov = r.providerWrapper(prov)
	}
	// 上限标在全部装饰之外：装饰钩子是开放的，标在里面会被它挡住。
	return withOutputLimit(prov, outputLimit), nil
}

// OutputLimiter 是能报告自己单次输出上限的 Provider：Router 给构造出的 provider 标上 spec
// 里的值（provider 配置的「单次最大输出」）。运行时的收尾据此请求输出预算——低于它，收尾
// 会比常规调用更容易被截断。
type OutputLimiter interface {
	OutputLimit() int
}

// OutputLimitOf 取 provider 的单次输出上限（token 数）；没标的返回 0。
func OutputLimitOf(p Provider) int {
	l, ok := p.(OutputLimiter)
	if !ok {
		return 0
	}
	return l.OutputLimit()
}

// limitedProvider 给 provider 标上单次输出上限，其余一律透传。
type limitedProvider struct {
	Provider
	limit int
}

func withOutputLimit(p Provider, limit int) Provider {
	return limitedProvider{Provider: p, limit: limit}
}

func (p limitedProvider) OutputLimit() int { return p.limit }

// resolve 按候选序（显式档 → 就近降级）找第一个可用的 provider spec。accept 为 nil 时
// 只要求 spec 可解析；非 nil 时还要求 accept(spec) 无错（如能构造出 client）。
//
// 任一候选不可用（被删、停用、密钥缺失、构造失败）都继续尝试相邻档——「配置 1 个
// provider 也能服务全部档位」，停用/缺钥的档位由相邻档承接。全部候选不可用时返回
// 最后一个候选的错误：若它属于「未配置」（停用、缺钥），哨兵链保留，任务据此干净终结；
// 无任何路由时返回带配置指引的 ErrNotConfigured。
func (r *Router) resolve(ctx context.Context, complexity Complexity, accept func(ProviderSpec) error) (ProviderSpec, error) {
	routing, err := r.store.GetRouting(ctx)
	if err != nil {
		return ProviderSpec{}, fmt.Errorf("provider/router: load routing: %w", err)
	}
	var lastErr error
	for _, lvl := range routeCandidates(complexity) {
		providerCode, ok := routing.Roles[string(lvl)]
		if !ok {
			continue
		}
		spec, err := r.store.GetProviderSpec(ctx, providerCode)
		if err != nil {
			lastErr = fmt.Errorf("provider/router: %s 档 provider %q 不可用: %w", lvl, providerCode, err)
			continue
		}
		if accept != nil {
			if err := accept(spec); err != nil {
				lastErr = fmt.Errorf("provider/router: %s 档 provider %q 无法构造 client: %w", lvl, providerCode, err)
				continue
			}
		}
		return spec, nil
	}
	if lastErr != nil {
		return ProviderSpec{}, lastErr
	}
	return ProviderSpec{}, fmt.Errorf(
		"provider/router: no LLM provider configured for complexity %q —"+
			"请在「模型」页接入 provider 并设置路由: %w", complexity, ErrNotConfigured)
}

// SetProviderWrapper 注入 Provider 装饰钩子（埋点/限流等），在首次 For() 之前调用。
// 包裹点在 retry 之外——记录"调用方视角"的调用。
func (r *Router) SetProviderWrapper(fn func(Provider) Provider) {
	r.mu.Lock()
	r.providerWrapper = fn
	r.dropCached() // 换钩子后作废旧缓存
	r.mu.Unlock()
}

// FailFast 让调用在两种「本可以等一等」的情况下都不等、立即交还：上游处于额度窗口，
// 或账户的在途名额已满。给没有外层重认领循环的调用方用（对话、问答、意图分类）：
// 智能体的调用方交还动作后会立即重新认领，需要调用层的等待把循环垫慢；对话侧等
// 下去（额度窗口内一分钟、名额排满时至多排掉预算的一半）只会把一句清楚的原因拖成
// 连接超时。在首次 For() 之前调用。
func (r *Router) FailFast() {
	r.governor.cooldown.holdInWindow = false
	r.governor.inflight.failFast = true
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

// resolveSpec 按候选序解析档位对应的 provider spec（显式档 → 就近降级，见 resolve）。
func (r *Router) resolveSpec(ctx context.Context, complexity Complexity) (ProviderSpec, error) {
	return r.resolve(ctx, complexity, nil)
}

// BuildGeneratorWithKey 用 ClientPool 共享 HTTP client 构造 Generator（按 provider 类型分派）。
// spec.APIKey 必须是已解析的明文密钥（解析归业务侧）。导出供业务策略层与 router 共用。
func BuildGeneratorWithKey(ctx context.Context, p ProviderSpec, pool *ClientPool) (Generator, error) {
	switch p.Type {
	case ProviderTypeAnthropicMessages:
		cli, err := pool.GetOrCreateAnthropicMessages(p.BaseURL, p.APIKey, p.AuthScheme)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Code, err)
		}
		return NewAnthropicMessages(ctx, p.Code, AnthropicMessagesConfig{
			BaseURL: p.BaseURL, Model: p.Model,
			Limit:           pool.OutputLimitFor(p.BaseURL, p.APIKey, p.Model, p.MaxTokens),
			ReasoningEffort: p.ReasoningEffort,
		}, cli)
	case ProviderTypeOpenAIChatCompletions, "":
		cli, err := pool.GetOrCreateOpenAI(p.BaseURL, p.APIKey, p.AuthScheme)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Code, err)
		}
		return NewOpenAIChatCompletions(ctx, p.Code, OpenAIChatCompletionsConfig{
			BaseURL: p.BaseURL, Model: p.Model,
			Limit:          pool.OutputLimitFor(p.BaseURL, p.APIKey, p.Model, p.MaxTokens),
			SupportsVision: p.SupportsVision, OutputLimitParam: p.OutputLimitParam,
			ReasoningEffort: p.ReasoningEffort,
		}, cli)
	case ProviderTypeOpenAIResponses:
		cli, err := pool.GetOrCreateHTTP(p.BaseURL, p.APIKey, p.AuthScheme)
		if err != nil {
			return nil, fmt.Errorf("provider %q: %w", p.Code, err)
		}
		return NewOpenAIResponses(ctx, p.Code, OpenAIResponsesConfig{
			BaseURL: p.BaseURL, Model: p.Model,
			Limit:           pool.OutputLimitFor(p.BaseURL, p.APIKey, p.Model, p.MaxTokens),
			SupportsVision:  p.SupportsVision,
			ReasoningEffort: p.ReasoningEffort,
		}, cli)
	}
	return nil, fmt.Errorf("provider %q 类型 %q 未知（支持: %s）", p.Code, p.Type, strings.Join(ProviderTypes, " / "))
}
