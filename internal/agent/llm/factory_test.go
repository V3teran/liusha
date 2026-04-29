package llm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/V3teran/liusha/internal/config"
)

// fakeGen 是测试用的桩 Generator。
type fakeGen struct {
	provider string
	model    string
}

func (g *fakeGen) Provider() string { return g.provider }
func (g *fakeGen) Model() string    { return g.model }
func (g *fakeGen) Generate(ctx context.Context, msgs []Message, _ []ToolSchema) (Result, error) {
	return Result{Provider: g.provider, Model: g.model}, nil
}

// newFakeBuilder 返回一个 Builder + 调用计数器，便于测懒加载缓存。
func newFakeBuilder() (Builder, *int64) {
	var count int64
	b := func(ctx context.Context, cfg config.Config, providerKey string, tools []ToolSchema) (Generator, error) {
		atomic.AddInt64(&count, 1)
		pc, ok := cfg.Providers[providerKey]
		if !ok {
			return nil, errors.New("unknown provider " + providerKey)
		}
		return &fakeGen{provider: providerKey, model: pc.DefaultModel}, nil
	}
	return b, &count
}

// 公共 cfg 构造：default=deepseek、light=anthropic_haiku、fallback=qwen
func makeRoutedCfg() config.Config {
	return config.Config{
		LLM: config.LLMConfig{
			DefaultProvider:  "deepseek",
			LightProvider:    "anthropic_haiku",
			FallbackProvider: "qwen",
			Routes: map[string]string{
				"react.main": "default_provider",
				"observer":   "light_provider",
				"distill":    "light_provider",
			},
		},
		Providers: map[string]config.ProviderConfig{
			"deepseek":        {DefaultModel: "deepseek-chat", APIKeyEnv: "DEEPSEEK_API_KEY"},
			"anthropic_haiku": {DefaultModel: "claude-haiku-4-5", APIKeyEnv: "ANTHROPIC_API_KEY"},
			"qwen":            {DefaultModel: "qwen3-max", APIKeyEnv: "QWEN_API_KEY"},
		},
	}
}

// TestFactory_For_ReactMain：routes 含 react.main -> default_provider -> deepseek
func TestFactory_For_ReactMain(t *testing.T) {
	cfg := makeRoutedCfg()
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g, err := f.For(context.Background(), "react.main", nil)
	if err != nil {
		t.Fatalf("For react.main 失败: %v", err)
	}
	if g.Provider() != "deepseek" {
		t.Errorf("provider 应为 deepseek，实际 %s", g.Provider())
	}
	if g.Model() != "deepseek-chat" {
		t.Errorf("model 应为 deepseek-chat，实际 %s", g.Model())
	}
}

// TestFactory_For_Observer：routes 含 observer -> light_provider -> anthropic_haiku
func TestFactory_For_Observer(t *testing.T) {
	cfg := makeRoutedCfg()
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g, err := f.For(context.Background(), "observer", nil)
	if err != nil {
		t.Fatalf("For observer 失败: %v", err)
	}
	if g.Provider() != "anthropic_haiku" {
		t.Errorf("provider 应为 anthropic_haiku，实际 %s", g.Provider())
	}
}

// TestFactory_For_UnknownRoleFallsBackToDefault：未在 routes 的 role 走 default_provider
func TestFactory_For_UnknownRoleFallsBackToDefault(t *testing.T) {
	cfg := makeRoutedCfg()
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g, err := f.For(context.Background(), "totally.unknown.role", nil)
	if err != nil {
		t.Fatalf("For 未知 role 失败: %v", err)
	}
	if g.Provider() != "deepseek" {
		t.Errorf("未知 role 应回退 default_provider=deepseek，实际 %s", g.Provider())
	}
}

// TestFactory_For_LazyCache：同一 role 调两次只构造一次
func TestFactory_For_LazyCache(t *testing.T) {
	cfg := makeRoutedCfg()
	builder, count := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g1, err := f.For(context.Background(), "react.main", nil)
	if err != nil {
		t.Fatalf("第一次 For 失败: %v", err)
	}
	g2, err := f.For(context.Background(), "react.main", nil)
	if err != nil {
		t.Fatalf("第二次 For 失败: %v", err)
	}
	if g1 != g2 {
		t.Errorf("懒加载缓存应返回同一实例")
	}
	if got := atomic.LoadInt64(count); got != 1 {
		t.Errorf("Builder 应仅被调用 1 次，实际 %d", got)
	}
}

// TestFactory_For_DifferentRolesSameProvider：两个 role 解析到同一 provider key 共享同一 Generator
func TestFactory_For_DifferentRolesSameProvider(t *testing.T) {
	cfg := makeRoutedCfg()
	builder, count := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g1, err := f.For(context.Background(), "observer", nil)
	if err != nil {
		t.Fatalf("For observer 失败: %v", err)
	}
	g2, err := f.For(context.Background(), "distill", nil)
	if err != nil {
		t.Fatalf("For distill 失败: %v", err)
	}
	if g1 != g2 {
		t.Errorf("observer / distill 都路由 light_provider，应共享缓存的 Generator")
	}
	if got := atomic.LoadInt64(count); got != 1 {
		t.Errorf("Builder 应仅被调用 1 次，实际 %d", got)
	}
}

// TestFactory_For_FallbackProviderRoute：route 指向 fallback_provider 解析到 qwen
func TestFactory_For_FallbackProviderRoute(t *testing.T) {
	cfg := makeRoutedCfg()
	cfg.LLM.Routes["retry"] = "fallback_provider"
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g, err := f.For(context.Background(), "retry", nil)
	if err != nil {
		t.Fatalf("For retry 失败: %v", err)
	}
	if g.Provider() != "qwen" {
		t.Errorf("retry route 应解析到 qwen，实际 %s", g.Provider())
	}
}

// TestFactory_For_EmptyTargetFieldFallsBack：route 指向未配置的 light_provider → 回退 default
func TestFactory_For_EmptyTargetFieldFallsBack(t *testing.T) {
	cfg := makeRoutedCfg()
	cfg.LLM.LightProvider = "" // 清空
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g, err := f.For(context.Background(), "observer", nil)
	if err != nil {
		t.Fatalf("For observer 失败: %v", err)
	}
	if g.Provider() != "deepseek" {
		t.Errorf("light_provider 为空时应回退 default=deepseek，实际 %s", g.Provider())
	}
}

// TestFactory_For_ConcurrentSameRole：并发调同一 role，Builder 仅被触发一次
func TestFactory_For_ConcurrentSameRole(t *testing.T) {
	cfg := makeRoutedCfg()
	builder, count := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	results := make([]Generator, N)
	for i := 0; i < N; i++ {
		i := i
		go func() {
			defer wg.Done()
			g, err := f.For(context.Background(), "react.main", nil)
			if err != nil {
				t.Errorf("并发 For 失败: %v", err)
				return
			}
			results[i] = g
		}()
	}
	wg.Wait()

	for i := 1; i < N; i++ {
		if results[i] != results[0] {
			t.Errorf("并发 For 应返回同一实例")
		}
	}
	if got := atomic.LoadInt64(count); got != 1 {
		t.Errorf("并发场景 Builder 应仅被调用 1 次，实际 %d", got)
	}
}

// TestBuildProvider_KnownProvidersBuildOK：表驱动校验 5 个 provider 都能构造
func TestBuildProvider_KnownProvidersBuildOK(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"deepseek":  {BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat", APIKeyEnv: "DEEPSEEK_API_KEY", MaxTokens: 4096},
			"anthropic": {BaseURL: "https://api.anthropic.com", DefaultModel: "claude-sonnet-4-6", APIKeyEnv: "ANTHROPIC_API_KEY", MaxTokens: 8192},
			"openai":    {BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4o", APIKeyEnv: "OPENAI_API_KEY", MaxTokens: 4096},
			"moonshot":  {BaseURL: "https://api.moonshot.cn/v1", DefaultModel: "kimi-k2-0905-preview", APIKeyEnv: "MOONSHOT_API_KEY", MaxTokens: 4096},
			"qwen":      {BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen3-max", APIKeyEnv: "QWEN_API_KEY", MaxTokens: 4096},
		},
	}
	for _, p := range []string{"deepseek", "anthropic", "openai", "moonshot", "qwen"} {
		t.Run(p, func(t *testing.T) {
			t.Setenv(cfg.Providers[p].APIKeyEnv, "fake-key")
			g, err := BuildProvider(context.Background(), cfg, p, nil)
			if err != nil {
				t.Fatalf("build %s: %v", p, err)
			}
			if g.Model() == "" {
				t.Fatalf("%s model empty", p)
			}
		})
	}
}

// TestBuildProvider_MissingKey：APIKey env 未设 → 报错
func TestBuildProvider_MissingKey(t *testing.T) {
	cfg := config.Config{Providers: map[string]config.ProviderConfig{
		"deepseek": {DefaultModel: "deepseek-chat", APIKeyEnv: "DEEPSEEK_API_KEY"},
	}}
	t.Setenv("DEEPSEEK_API_KEY", "")
	if _, err := BuildProvider(context.Background(), cfg, "deepseek", nil); err == nil {
		t.Fatal("应在 key 为空时报错")
	}
}

// TestBuildProvider_UnknownProvider：未知 provider key 报错
func TestBuildProvider_UnknownProvider(t *testing.T) {
	cfg := config.Config{Providers: map[string]config.ProviderConfig{}}
	if _, err := BuildProvider(context.Background(), cfg, "no_such_provider", nil); err == nil {
		t.Fatal("未知 provider 应报错")
	}
}
