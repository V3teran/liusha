package llm

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/config/llmcfg"
)

// routerResolver：planner→primary、inspector→light、__default__→primary、__fallback__→fb。
// 路由解析已下沉到 Resolver（运行期多级缓存），Router 只负责 retry/fallback 装配。
func routerResolver() fakeResolver {
	primary := prov("primary", "primary-model", "K1")
	return fakeResolver{
		byRole: map[string]llmcfg.Provider{
			"planner": primary,
			"inspector":    prov("light", "light-model", "K2"),
		},
		def:    primary,
		hasDef: true,
		fb:     prov("fb", "fb-model", "K3"),
		hasFB:  true,
	}
}

// builderFromMap：用预置 Generator map 构造 Builder（按 provider key 分发）。
func builderFromMap(gens map[string]Generator) Builder {
	return func(_ context.Context, p llmcfg.Provider, _ *ClientPool) (Generator, error) {
		if g, ok := gens[p.Key]; ok {
			return g, nil
		}
		return &testGen{tag: p.Key, model: p.Key + "-model"}, nil
	}
}

// TestRouter_For_RoutesAndWraps：For("planner") 应解析到 primary 且经 retry 装饰
func TestRouter_For_RoutesAndWraps(t *testing.T) {
	primary := &testGen{tag: "primary", model: "primary-model"}
	light := &testGen{tag: "light", model: "light-model"}
	fb := &testGen{tag: "fb", model: "fb-model"}
	builder := builderFromMap(map[string]Generator{
		"primary": primary, "light": light, "fb": fb,
	})
	factory := NewFactoryWithBuilder(routerResolver(), builder)
	r := NewRouter(factory)

	g, err := r.For(context.Background(), "planner")
	if err != nil {
		t.Fatalf("For planner 失败: %v", err)
	}
	if _, ok := g.(*retryGen); !ok {
		t.Errorf("Router.For 返回的 Generator 应是 *retryGen（被 retry 装饰），实际 %T", g)
	}
	if g.Provider() != "primary" {
		t.Errorf("Provider 应透传到 primary，实际 %s", g.Provider())
	}
}

// TestRouter_For_InspectorRoutesLight：inspector 应解析到 light
func TestRouter_For_InspectorRoutesLight(t *testing.T) {
	factory := NewFactoryWithBuilder(routerResolver(), builderFromMap(nil))
	r := NewRouter(factory)

	g, err := r.For(context.Background(), "inspector")
	if err != nil {
		t.Fatalf("For inspector 失败: %v", err)
	}
	if g.Provider() != "light" {
		t.Errorf("inspector 应路由 light，实际 %s", g.Provider())
	}
}

// TestRouter_For_UnknownFallsBackToDefault：未列出 role 走 default（resolver 兜底）
func TestRouter_For_UnknownFallsBackToDefault(t *testing.T) {
	factory := NewFactoryWithBuilder(routerResolver(), builderFromMap(nil))
	r := NewRouter(factory)

	g, err := r.For(context.Background(), "totally.unknown")
	if err != nil {
		t.Fatalf("For unknown 失败: %v", err)
	}
	if g.Provider() != "primary" {
		t.Errorf("未知 role 应回退 default=primary，实际 %s", g.Provider())
	}
}

// TestRouter_For_FallbackTriggers：primary 4 次 429 → fallback(fb) 兜底成功
func TestRouter_For_FallbackTriggers(t *testing.T) {
	primary := &testGen{tag: "primary", model: "primary-model", seq: []error{
		&HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429},
	}}
	fb := &testGen{tag: "fb", model: "fb-model"}
	builder := builderFromMap(map[string]Generator{
		"primary": primary, "fb": fb,
	})
	factory := NewFactoryWithBuilder(routerResolver(), builder)
	r := NewRouterWithOptions(factory, noSleep())

	g, err := r.For(context.Background(), "planner")
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

// TestRouter_For_NoFallbackConfigured：__fallback__ 角色未绑定时不注入 fallback，primary 耗尽即抛
func TestRouter_For_NoFallbackConfigured(t *testing.T) {
	primary := &testGen{tag: "primary", model: "primary-model", seq: []error{
		&HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429}, &HTTPError{Code: 429},
	}}
	// 无 fallback 的 resolver：ProviderForFallback 报错 → Router 静默降级
	res := routerResolver()
	res.hasFB = false
	builder := builderFromMap(map[string]Generator{"primary": primary})
	factory := NewFactoryWithBuilder(res, builder)
	r := NewRouterWithOptions(factory, noSleep())

	g, err := r.For(context.Background(), "planner")
	if err != nil {
		t.Fatalf("For 失败: %v", err)
	}
	if _, err := g.Generate(context.Background(), nil, nil); err == nil {
		t.Fatal("无 fallback 时应抛错")
	}
}
