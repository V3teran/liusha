package llm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/V3teran/liusha/internal/config/llm"
)

// newFakeBuilder 返回一个 Builder + 调用计数器，便于断言 builder 被调几次。
//
// T11 后 Factory 不再缓存 Generator，每次 For 都触发 builder。
func newFakeBuilder() (Builder, *int64) {
	var count int64
	b := func(_ context.Context, p llmcfg.Provider, _ *ClientPool) (Generator, error) {
		atomic.AddInt64(&count, 1)
		return &testGen{
			provider: p.Key,
			model:    p.DefaultModel,
			res:      Result{Provider: p.Key, Model: p.DefaultModel},
		}, nil
	}
	return b, &count
}

// prov 构造一个最小 provider 部署行（测试用）。
func prov(key, model, env string) llmcfg.Provider {
	return llmcfg.Provider{Key: key, DefaultModel: model, APIKeyEnv: env}
}

// fakeResolver 把 role 映射到 provider 部署（测试注入，替代真实 llmstore 的多级缓存解析）。
// def/hasDef 模拟 __default__ 兜底：未命中 role 时返 def。fb/hasFB 模拟全局备胎 __fallback__。
type fakeResolver struct {
	byRole map[string]llmcfg.Provider
	def    llmcfg.Provider
	hasDef bool
	fb     llmcfg.Provider
	hasFB  bool
}

func (r fakeResolver) ProviderForRole(_ context.Context, role string) (llmcfg.Provider, error) {
	if p, ok := r.byRole[role]; ok {
		return p, nil
	}
	if r.hasDef {
		return r.def, nil
	}
	return llmcfg.Provider{}, errors.New("unresolved role " + role)
}

func (r fakeResolver) ProviderForFallback(_ context.Context) (llmcfg.Provider, error) {
	if r.hasFB {
		return r.fb, nil
	}
	return llmcfg.Provider{}, errors.New("unresolved fallback")
}

// makeResolver：planner→deepseek、inspector→anthropic_haiku、__default__ 兜底→deepseek、
// __fallback__ 备胎→qwen（角色路由解析的正确性由 llmstore 单测覆盖，这里只喂结果）。
func makeResolver() fakeResolver {
	deepseek := prov("deepseek", "deepseek-chat", "DEEPSEEK_API_KEY")
	return fakeResolver{
		byRole: map[string]llmcfg.Provider{
			"planner":   deepseek,
			"inspector": prov("anthropic_haiku", "claude-haiku-4-5", "ANTHROPIC_API_KEY"),
		},
		def:    deepseek,
		hasDef: true,
		fb:     prov("qwen", "qwen3-max", "QWEN_API_KEY"),
		hasFB:  true,
	}
}

// TestFactory_For_ReactMain：planner 解析到 deepseek
func TestFactory_For_ReactMain(t *testing.T) {
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(makeResolver(), builder)

	g, err := f.For(context.Background(), "planner")
	if err != nil {
		t.Fatalf("For planner 失败: %v", err)
	}
	if g.Provider() != "deepseek" {
		t.Errorf("provider 应为 deepseek，实际 %s", g.Provider())
	}
	if g.Model() != "deepseek-chat" {
		t.Errorf("model 应为 deepseek-chat，实际 %s", g.Model())
	}
}

// TestFactory_For_Inspector：inspector 解析到 anthropic_haiku
func TestFactory_For_Inspector(t *testing.T) {
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(makeResolver(), builder)

	g, err := f.For(context.Background(), "inspector")
	if err != nil {
		t.Fatalf("For inspector 失败: %v", err)
	}
	if g.Provider() != "anthropic_haiku" {
		t.Errorf("provider 应为 anthropic_haiku，实际 %s", g.Provider())
	}
}

// TestFactory_For_UnknownRoleFallsBackToDefault：未命中 role 走 default 别名（resolver 兜底）
func TestFactory_For_UnknownRoleFallsBackToDefault(t *testing.T) {
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(makeResolver(), builder)

	g, err := f.For(context.Background(), "totally.unknown.role")
	if err != nil {
		t.Fatalf("For 未知 role 失败: %v", err)
	}
	if g.Provider() != "deepseek" {
		t.Errorf("未知 role 应回退 default=deepseek，实际 %s", g.Provider())
	}
}

// TestFactory_For_ResolveError：resolver 报错（路由未配置）时 For 透传错误，不静默兜底
func TestFactory_For_ResolveError(t *testing.T) {
	builder, _ := newFakeBuilder()
	// 无 default 兜底的 resolver：未命中 role 直接报错
	r := fakeResolver{byRole: map[string]llmcfg.Provider{}}
	f := NewFactoryWithBuilder(r, builder)

	if _, err := f.For(context.Background(), "anything"); err == nil {
		t.Fatal("resolver 解析失败时 For 应报错")
	}
}

// TestFactory_For_NoCache：T11 起 Factory 不再缓存 Generator——每次 For 调 builder 一次。
func TestFactory_For_NoCache(t *testing.T) {
	builder, count := newFakeBuilder()
	f := NewFactoryWithBuilder(makeResolver(), builder)

	g1, err := f.For(context.Background(), "planner")
	if err != nil {
		t.Fatalf("第一次 For 失败: %v", err)
	}
	g2, err := f.For(context.Background(), "planner")
	if err != nil {
		t.Fatalf("第二次 For 失败: %v", err)
	}
	if g1 == g2 {
		t.Errorf("T11 后 Factory 不再缓存，应返回不同实例")
	}
	if got := atomic.LoadInt64(count); got != 2 {
		t.Errorf("Builder 应被调 2 次，实际 %d", got)
	}
}

// TestFactory_For_ConcurrentSafe：并发调同一 role 不应 panic / data race。
func TestFactory_For_ConcurrentSafe(t *testing.T) {
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(makeResolver(), builder)

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := f.For(context.Background(), "planner"); err != nil {
				t.Errorf("并发 For 失败: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestBuildProvider_KnownProvidersBuildOK：表驱动校验 5 个 provider 部署都能构造
func TestBuildProvider_KnownProvidersBuildOK(t *testing.T) {
	providers := []llmcfg.Provider{
		{Key: "deepseek", BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat", APIKeyEnv: "DEEPSEEK_API_KEY", MaxTokens: 4096, SupportsVision: false},
		{Key: "anthropic", Type: ProviderTypeAnthropic, BaseURL: "https://api.anthropic.com", DefaultModel: "claude-sonnet-4-6", APIKeyEnv: "ANTHROPIC_API_KEY", MaxTokens: 8192, SupportsVision: true},
		{Key: "openai", BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4o", APIKeyEnv: "OPENAI_API_KEY", MaxTokens: 4096, SupportsVision: true},
		{Key: "moonshot", BaseURL: "https://api.moonshot.cn/v1", DefaultModel: "kimi-k2-0905-preview", APIKeyEnv: "MOONSHOT_API_KEY", MaxTokens: 4096, SupportsVision: false},
		{Key: "qwen", BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen3-max", APIKeyEnv: "QWEN_API_KEY", MaxTokens: 4096, SupportsVision: false},
	}
	pool := NewClientPool()
	for _, p := range providers {
		t.Run(p.Key, func(t *testing.T) {
			t.Setenv(p.APIKeyEnv, "fake-key")
			g, err := BuildProvider(context.Background(), p, pool)
			if err != nil {
				t.Fatalf("build %s: %v", p.Key, err)
			}
			if g.Model() == "" {
				t.Fatalf("%s model empty", p.Key)
			}
		})
	}
}

// TestBuildProvider_MissingKey：APIKey env 未设 → 报错
func TestBuildProvider_MissingKey(t *testing.T) {
	p := prov("deepseek", "deepseek-chat", "DEEPSEEK_API_KEY")
	t.Setenv("DEEPSEEK_API_KEY", "")
	if _, err := BuildProvider(context.Background(), p, NewClientPool()); err == nil {
		t.Fatal("应在 key 为空时报错")
	}
}

// TestBuildProvider_UnknownType：未知 provider type 报错
func TestBuildProvider_UnknownType(t *testing.T) {
	p := llmcfg.Provider{Key: "weird", Type: "no_such_type", DefaultModel: "m", APIKeyEnv: "WEIRD_KEY"}
	t.Setenv("WEIRD_KEY", "fake-key")
	if _, err := BuildProvider(context.Background(), p, NewClientPool()); err == nil {
		t.Fatal("未知 type 应报错")
	}
}
