package llm

import (
	"context"
	"fmt"
	"testing"
)

// fakeRouterStore 是 RouterStore 的内存实现：routes 缺档/ spec 缺失均可构造，
// 用于验证档位的就近降级解析。
type fakeRouterStore struct {
	routes    map[string]string      // role → provider key
	providers map[string]ProviderSpec // key → spec（缺 key = provider 不可用）
}

func (f *fakeRouterStore) GetRouting(_ context.Context) (RoutingSpec, error) {
	r := RoutingSpec{Roles: map[string]string{}}
	for k, v := range f.routes {
		r.Roles[k] = v
	}
	return r, nil
}

func (f *fakeRouterStore) GetProviderSpec(_ context.Context, key string) (ProviderSpec, error) {
	if p, ok := f.providers[key]; ok {
		return p, nil
	}
	return ProviderSpec{}, fmt.Errorf("provider %q not found", key)
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
