package llm

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/config"
)

// routerCfg：default=primary、fallback=fb；routes 含 react.main / observer
func routerCfg() config.Config {
	return config.Config{
		LLM: config.LLMConfig{
			DefaultProvider:  "primary",
			LightProvider:    "light",
			FallbackProvider: "fb",
			Routes: map[string]string{
				"react_main": "default_provider",
				"observer":   "light_provider",
			},
		},
		Providers: map[string]config.ProviderConfig{
			"primary": {DefaultModel: "primary-model", APIKeyEnv: "K1"},
			"light":   {DefaultModel: "light-model", APIKeyEnv: "K2"},
			"fb":      {DefaultModel: "fb-model", APIKeyEnv: "K3"},
		},
	}
}

// builderFromMap：用预置 mockGen map 构造 Builder（按 providerKey 分发）。
func builderFromMap(gens map[string]Generator) Builder {
	return func(ctx context.Context, cfg config.Config, providerKey string, tools []ToolSchema) (Generator, error) {
		if g, ok := gens[providerKey]; ok {
			return g, nil
		}
		return &mockGen{tag: providerKey, model: providerKey + "-model"}, nil
	}
}

// TestRouter_For_RoutesAndWraps：For("react_main") 应解析到 default(primary) 且经 retry 装饰
func TestRouter_For_RoutesAndWraps(t *testing.T) {
	cfg := routerCfg()
	primary := &mockGen{tag: "primary", model: "primary-model"}
	light := &mockGen{tag: "light", model: "light-model"}
	fb := &mockGen{tag: "fb", model: "fb-model"}
	builder := builderFromMap(map[string]Generator{
		"primary": primary, "light": light, "fb": fb,
	})
	factory := NewFactoryWithBuilder(cfg, builder)
	r := NewRouter(factory)

	g, err := r.For(context.Background(), "react_main", nil)
	if err != nil {
		t.Fatalf("For react.main 失败: %v", err)
	}
	if _, ok := g.(*retryGen); !ok {
		t.Errorf("Router.For 返回的 Generator 应是 *retryGen（被 retry 装饰），实际 %T", g)
	}
	if g.Provider() != "primary" {
		t.Errorf("Provider 应透传到 primary，实际 %s", g.Provider())
	}
}

// TestRouter_For_ObserverRoutesLight：observer 应解析到 light
func TestRouter_For_ObserverRoutesLight(t *testing.T) {
	cfg := routerCfg()
	builder := builderFromMap(nil)
	factory := NewFactoryWithBuilder(cfg, builder)
	r := NewRouter(factory)

	g, err := r.For(context.Background(), "observer", nil)
	if err != nil {
		t.Fatalf("For observer 失败: %v", err)
	}
	if g.Provider() != "light" {
		t.Errorf("observer 应路由 light，实际 %s", g.Provider())
	}
}

// TestRouter_For_UnknownFallsBackToDefault：未列出 role 走 default
func TestRouter_For_UnknownFallsBackToDefault(t *testing.T) {
	cfg := routerCfg()
	builder := builderFromMap(nil)
	factory := NewFactoryWithBuilder(cfg, builder)
	r := NewRouter(factory)

	g, err := r.For(context.Background(), "totally.unknown", nil)
	if err != nil {
		t.Fatalf("For unknown 失败: %v", err)
	}
	if g.Provider() != "primary" {
		t.Errorf("未知 role 应回退 default=primary，实际 %s", g.Provider())
	}
}

// TestRouter_For_FallbackTriggers：primary 4 次 429 → fallback(fb) 兜底成功
func TestRouter_For_FallbackTriggers(t *testing.T) {
	cfg := routerCfg()
	primary := &mockGen{tag: "primary", model: "primary-model", seq: []error{
		&HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429},
	}}
	fb := &mockGen{tag: "fb", model: "fb-model"}
	builder := builderFromMap(map[string]Generator{
		"primary": primary, "fb": fb,
	})
	factory := NewFactoryWithBuilder(cfg, builder)
	r := NewRouterWithOptions(factory, noSleep())

	g, err := r.For(context.Background(), "react_main", nil)
	if err != nil {
		t.Fatalf("For 失败: %v", err)
	}
	res, err := g.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("应由 fallback 兜底成功，实际 err=%v", err)
	}
	if res.Content != "ok-fb" {
		t.Errorf("应来自 fallback，实际 %q", res.Content)
	}
	if primary.calls != 4 {
		t.Errorf("primary 应被调 4 次，实际 %d", primary.calls)
	}
	if fb.calls != 1 {
		t.Errorf("fallback 应被调 1 次，实际 %d", fb.calls)
	}
}

// TestRouter_For_NoFallbackConfigured：FallbackProvider 为空时不注入 fallback
func TestRouter_For_NoFallbackConfigured(t *testing.T) {
	cfg := routerCfg()
	cfg.LLM.FallbackProvider = ""
	primary := &mockGen{tag: "primary", model: "primary-model", seq: []error{
		&HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429},
	}}
	builder := builderFromMap(map[string]Generator{"primary": primary})
	factory := NewFactoryWithBuilder(cfg, builder)
	r := NewRouterWithOptions(factory, noSleep())

	g, err := r.For(context.Background(), "react_main", nil)
	if err != nil {
		t.Fatalf("For 失败: %v", err)
	}
	if _, err := g.Generate(context.Background(), nil, nil); err == nil {
		t.Fatal("无 fallback 时应抛错")
	}
}
