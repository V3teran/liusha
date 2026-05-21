package llm

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/V3teran/liusha/internal/config"
)

// newFakeBuilder 返回一个 Builder + 调用计数器，便于断言 builder 被调几次。
//
// T11 后 Factory 不再缓存 Generator，每次 For 都触发 builder。
func newFakeBuilder() (Builder, *int64) {
	var count int64
	b := func(_ context.Context, cfg config.Config, providerKey string, _ *ClientPool) (Generator, error) {
		atomic.AddInt64(&count, 1)
		pc, ok := cfg.Providers[providerKey]
		if !ok {
			return nil, errors.New("unknown provider " + providerKey)
		}
		return &testGen{
			provider: providerKey,
			model:    pc.DefaultModel,
			res:      Result{Provider: providerKey, Model: pc.DefaultModel},
		}, nil
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
			Agents: map[string]string{
				"commander": "default_provider",
				"inspector":     "light_provider",
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

	g, err := f.For(context.Background(), "commander")
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

// TestFactory_For_Inspector：routes 含 inspector -> light_provider -> anthropic_haiku
func TestFactory_For_Inspector(t *testing.T) {
	cfg := makeRoutedCfg()
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g, err := f.For(context.Background(), "inspector")
	if err != nil {
		t.Fatalf("For inspector 失败: %v", err)
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

	g, err := f.For(context.Background(), "totally.unknown.role")
	if err != nil {
		t.Fatalf("For 未知 role 失败: %v", err)
	}
	if g.Provider() != "deepseek" {
		t.Errorf("未知 role 应回退 default_provider=deepseek，实际 %s", g.Provider())
	}
}

// TestFactory_For_NoCache：T11 起 Factory 不再缓存 Generator——每次 For 调 builder 一次。
//
// 同 role 两次调用应返回新实例（不同指针），builder 被调 2 次。
func TestFactory_For_NoCache(t *testing.T) {
	cfg := makeRoutedCfg()
	builder, count := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g1, err := f.For(context.Background(), "commander")
	if err != nil {
		t.Fatalf("第一次 For 失败: %v", err)
	}
	g2, err := f.For(context.Background(), "commander")
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

// TestFactory_For_FallbackProviderRoute：route 指向 fallback_provider 解析到 qwen
func TestFactory_For_FallbackProviderRoute(t *testing.T) {
	cfg := makeRoutedCfg()
	if cfg.LLM.Agents == nil {
		cfg.LLM.Agents = map[string]string{}
	}
	cfg.LLM.Agents["retry"] = "fallback_provider"
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	g, err := f.For(context.Background(), "retry")
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

	g, err := f.For(context.Background(), "inspector")
	if err != nil {
		t.Fatalf("For inspector 失败: %v", err)
	}
	if g.Provider() != "deepseek" {
		t.Errorf("light_provider 为空时应回退 default=deepseek，实际 %s", g.Provider())
	}
}

// TestFactory_For_ConcurrentSafe：并发调同一 role 不应 panic / data race。
//
// T11 后 Factory 不再持有 Generator 缓存（无锁），底层 ClientPool 自带 mutex；
// 这里仅验证并发调用不出错，不再断言 builder 调用次数。
func TestFactory_For_ConcurrentSafe(t *testing.T) {
	cfg := makeRoutedCfg()
	builder, _ := newFakeBuilder()
	f := NewFactoryWithBuilder(cfg, builder)

	const N = 32
	var wg sync.WaitGroup
	wg.Add(N)
	for i := 0; i < N; i++ {
		go func() {
			defer wg.Done()
			if _, err := f.For(context.Background(), "commander"); err != nil {
				t.Errorf("并发 For 失败: %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestBuildProvider_KnownProvidersBuildOK：表驱动校验 5 个 provider 都能构造
func TestBuildProvider_KnownProvidersBuildOK(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderConfig{
			"deepseek":  {BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat", APIKeyEnv: "DEEPSEEK_API_KEY", MaxTokens: 4096},
			"anthropic": {Type: ProviderTypeAnthropic, BaseURL: "https://api.anthropic.com", DefaultModel: "claude-sonnet-4-6", APIKeyEnv: "ANTHROPIC_API_KEY", MaxTokens: 8192},
			"openai":    {BaseURL: "https://api.openai.com/v1", DefaultModel: "gpt-4o", APIKeyEnv: "OPENAI_API_KEY", MaxTokens: 4096},
			"moonshot":  {BaseURL: "https://api.moonshot.cn/v1", DefaultModel: "kimi-k2-0905-preview", APIKeyEnv: "MOONSHOT_API_KEY", MaxTokens: 4096},
			"qwen":      {BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1", DefaultModel: "qwen3-max", APIKeyEnv: "QWEN_API_KEY", MaxTokens: 4096},
		},
	}
	pool := NewClientPool()
	for _, p := range []string{"deepseek", "anthropic", "openai", "moonshot", "qwen"} {
		t.Run(p, func(t *testing.T) {
			t.Setenv(cfg.Providers[p].APIKeyEnv, "fake-key")
			g, err := BuildProvider(context.Background(), cfg, p, pool)
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
	if _, err := BuildProvider(context.Background(), cfg, "deepseek", NewClientPool()); err == nil {
		t.Fatal("应在 key 为空时报错")
	}
}

// TestBuildProvider_UnknownProvider：未知 provider key 报错
func TestBuildProvider_UnknownProvider(t *testing.T) {
	cfg := config.Config{Providers: map[string]config.ProviderConfig{}}
	if _, err := BuildProvider(context.Background(), cfg, "no_such_provider", NewClientPool()); err == nil {
		t.Fatal("未知 provider 应报错")
	}
}
