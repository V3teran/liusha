package llm

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

// fakeRouterStore 是 RouterStore 的内存实现：routes 缺档/ spec 缺失均可构造，
// 用于验证档位的就近降级解析。
type fakeRouterStore struct {
	routes    map[string]string       // 档位 → provider code
	providers map[string]ProviderSpec // code → spec（缺 code = provider 不可用）
}

func (f *fakeRouterStore) GetRouting(_ context.Context) (RoutingSpec, error) {
	r := RoutingSpec{Roles: map[string]string{}}
	for k, v := range f.routes {
		r.Roles[k] = v
	}
	return r, nil
}

func (f *fakeRouterStore) GetProviderSpec(_ context.Context, code string) (ProviderSpec, error) {
	if p, ok := f.providers[code]; ok {
		return p, nil
	}
	return ProviderSpec{}, fmt.Errorf("provider %q not found", code)
}

func goodSpec(code string) ProviderSpec {
	return ProviderSpec{
		Code: code, Type: ProviderTypeOpenAICompat,
		BaseURL: "http://127.0.0.1:1", Model: "m-" + code, APIKey: "k-" + code,
		MaxTokens: 16,
	}
}

// 档位就近降级：只配 1 个 provider（仅路由 medium）时，
// For(complex) / For(simple) 自动落到 medium 的 provider——「配 1 把 key 服务全档」。
func TestRouter_DegradeToAdjacentLevel(t *testing.T) {
	r := NewRouter(&fakeRouterStore{
		routes:    map[string]string{"medium": "glm"},
		providers: map[string]ProviderSpec{"glm": goodSpec("glm")},
	})
	for _, lvl := range []Complexity{ComplexityComplex, ComplexityMedium, ComplexitySimple} {
		p, err := r.For(context.Background(), lvl)
		if err != nil {
			t.Fatalf("For(%s) 应降级到 medium: %v", lvl, err)
		}
		_ = p
	}
}

// 显式档优先：simple 显式路由到 cheap 时，For(simple) 不降级到 medium。
func TestRouter_ExplicitRouteWins(t *testing.T) {
	r := NewRouter(&fakeRouterStore{
		routes:    map[string]string{"simple": "cheap", "medium": "strong"},
		providers: map[string]ProviderSpec{"cheap": goodSpec("cheap"), "strong": goodSpec("strong")},
	})
	if _, err := r.For(context.Background(), ComplexitySimple); err != nil {
		t.Fatalf("For(simple): %v", err)
	}
	// 候选序验证：cheap 可用即命中（若误降级到 medium 也能成功——通过 ContextWindow
	// 区分 spec 来源，cheap 的 Model 编码了 code）。
	spec, err := r.resolveSpec(context.Background(), ComplexitySimple)
	if err != nil {
		t.Fatalf("resolveSpec: %v", err)
	}
	if spec.Code != "cheap" {
		t.Errorf("显式路由应优先，got %q", spec.Code)
	}
}

// 空路由（用户什么都没配）→ 明确报错，错误信息含补齐指引。
func TestRouter_NothingConfigured(t *testing.T) {
	r := NewRouter(&fakeRouterStore{routes: map[string]string{}, providers: map[string]ProviderSpec{}})
	_, err := r.For(context.Background(), ComplexityMedium)
	if err == nil {
		t.Fatal("零路由应报错")
	}
	if !contains(err.Error(), "no LLM provider configured") {
		t.Errorf("错误应含补齐指引: %v", err)
	}
}

// provider 被删（路由悬空）→ 降级到相邻档的可用 provider。
func TestRouter_DanglingRouteDegrades(t *testing.T) {
	r := NewRouter(&fakeRouterStore{
		routes:    map[string]string{"complex": "ghost", "medium": "glm"},
		providers: map[string]ProviderSpec{"glm": goodSpec("glm")},
	})
	spec, err := r.resolveSpec(context.Background(), ComplexityComplex)
	if err != nil {
		t.Fatalf("悬空路由应降级: %v", err)
	}
	if spec.Code != "glm" {
		t.Errorf("complex 应降级到 medium 的 glm，got %q", spec.Code)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// notConfiguredStore 在 fakeRouterStore 之上把指定 key 报为「未配置」（停用/缺钥）。
type notConfiguredStore struct {
	fakeRouterStore
	unavailable map[string]bool
}

func (s *notConfiguredStore) GetProviderSpec(ctx context.Context, code string) (ProviderSpec, error) {
	if s.unavailable[code] {
		return ProviderSpec{}, fmt.Errorf("%w: provider %q 已停用", ErrNotConfigured, code)
	}
	return s.fakeRouterStore.GetProviderSpec(ctx, code)
}

// 显式档的 provider 被停用（或缺钥）时降级到相邻档，而不是直接让该档报错。
func TestRouter_UnavailableProviderDegradesToAdjacentLevel(t *testing.T) {
	r := NewRouter(&notConfiguredStore{
		fakeRouterStore: fakeRouterStore{
			routes:    map[string]string{"complex": "off", "medium": "glm"},
			providers: map[string]ProviderSpec{"glm": goodSpec("glm")},
		},
		unavailable: map[string]bool{"off": true},
	})
	spec, err := r.resolveSpec(context.Background(), ComplexityComplex)
	if err != nil {
		t.Fatalf("complex 档 provider 停用应降级到 medium: %v", err)
	}
	if spec.Code != "glm" {
		t.Fatalf("应解析到 medium 的 glm，got %q", spec.Code)
	}
	if _, err := r.For(context.Background(), ComplexityComplex); err != nil {
		t.Fatalf("For(complex) 应降级成功: %v", err)
	}
}

// 全部候选都「未配置」时保留哨兵链：任务据此干净终结，而非当作瞬时故障无限重试。
func TestRouter_AllUnavailableIsNotConfigured(t *testing.T) {
	r := NewRouter(&notConfiguredStore{
		fakeRouterStore: fakeRouterStore{routes: map[string]string{"medium": "off"}},
		unavailable:     map[string]bool{"off": true},
	})
	_, err := r.For(context.Background(), ComplexityMedium)
	if !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("全部停用应是 ErrNotConfigured，got %v", err)
	}
}

// 部署方配置的重试次数原样生效：写 0 就是该类错误不重试，不被换成内置值。
func TestNewRouterWithOpts_HonorsZeroRetries(t *testing.T) {
	r := NewRouterWithOpts(&fakeRouterStore{routes: map[string]string{}, providers: map[string]ProviderSpec{}}, RetryOptions{}, 0)
	if r.retryOpts.MaxRetries429 != 0 || r.retryOpts.MaxRetries529 != 0 || r.retryOpts.MaxRetries5xx != 0 || r.retryOpts.MaxRetriesNet != 0 {
		t.Fatalf("重试次数被改写: %+v", r.retryOpts)
	}
	if d := NewRouter(&fakeRouterStore{routes: map[string]string{}, providers: map[string]ProviderSpec{}}).retryOpts; d.MaxRetries429 != DefaultRetryOptions().MaxRetries429 {
		t.Fatalf("无配置的构造应使用内置策略: %+v", d)
	}
}

// 账户并发上限经 Router 落到每个 Provider 实例上，并且按账户归属生效：共用一把密钥的
// 两个 provider 是同一份额度，换了密钥的另一个 provider 不受约束。
func TestRouter_AccountConcurrencyIsEnforcedPerAccount(t *testing.T) {
	shared := goodSpec("a")
	sibling := goodSpec("a2")
	sibling.APIKey = shared.APIKey // 同一服务地址、同一把密钥：同一个账户
	other := goodSpec("b")         // 另一把密钥：另一个账户
	r := NewRouterWithOpts(&fakeRouterStore{
		routes:    map[string]string{"simple": "a", "complex": "a2", "medium": "b"},
		providers: map[string]ProviderSpec{"a": shared, "a2": sibling, "b": other},
	}, RetryOptions{}, 1)
	r.governor.inflight.maxWait = 20 * time.Millisecond
	ctx, cancel := giveUpSoon() // 预算小：排不到名额的调用约 80ms 就放弃
	defer cancel()

	hold, err := r.governor.inflight.acquire(ctx, quotaIdentity(shared.BaseURL, shared.APIKey), false)
	if err != nil {
		t.Fatal(err)
	}
	defer hold()

	for _, lvl := range []Complexity{ComplexitySimple, ComplexityComplex} {
		p, err := r.For(ctx, lvl)
		if err != nil {
			t.Fatalf("For(%s): %v", lvl, err)
		}
		if _, err := p.Complete(ctx, Request{}); !errors.Is(err, ErrInflightBusy) {
			t.Fatalf("For(%s) 与被占住的账户共用密钥，应排不到名额，实际 %v", lvl, err)
		}
	}
	// 另一个账户不受约束：请求真的发了出去（落到不可达的测试端点上，以网络错误返回）。
	p, err := r.For(ctx, ComplexityMedium)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.Complete(ctx, Request{}); err == nil || errors.Is(err, ErrInflightBusy) {
		t.Fatalf("另一个账户的调用应照常发出，实际 %v", err)
	}
}

// 对话侧没有外层重认领循环：两种「本可以等一等」的情况——账户的在途名额已满、上游处于
// 额度窗口——都不等，立即交还。等下去（名额排满时至多排掉预算的一半、额度窗口内至多
// 一分钟）只会把一句清楚的原因拖成连接超时。
func TestRouter_FailFastReturnsInsteadOfWaiting(t *testing.T) {
	spec := goodSpec("a")
	r := NewRouterWithOpts(&fakeRouterStore{
		routes:    map[string]string{"simple": "a"},
		providers: map[string]ProviderSpec{"a": spec},
	}, RetryOptions{}, 1)
	r.FailFast()
	account := quotaIdentity(spec.BaseURL, spec.APIKey)
	ctx, cancel := withBudget(10 * time.Second) // 预算很宽：要等的话等得到超时
	defer cancel()
	p, err := r.For(ctx, ComplexitySimple)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("名额已满", func(t *testing.T) {
		hold, err := r.governor.inflight.acquire(ctx, account, false)
		if err != nil {
			t.Fatal(err)
		}
		defer hold()

		start := time.Now()
		_, err = p.Complete(ctx, Request{})
		if !errors.Is(err, ErrInflightBusy) {
			t.Fatalf("应因排不到在途名额而交还，实际 %v", err)
		}
		if waited := time.Since(start); waited >= 2*time.Second {
			t.Fatalf("不应为名额排队，实际等了 %v", waited)
		}
	})

	t.Run("额度窗口", func(t *testing.T) {
		r.governor.cooldown.open(account, time.Now().Add(time.Hour), quotaError(time.Hour), time.Now())

		start := time.Now()
		_, err := p.Complete(ctx, Request{})
		assertNeverSent(t, err)
		if waited := time.Since(start); waited >= 2*time.Second {
			t.Fatalf("不应在额度窗口内等候，实际等了 %v", waited)
		}
	})
}
