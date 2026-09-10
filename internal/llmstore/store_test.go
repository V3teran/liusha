package llmstore

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/config/llm"
)

// fakeLLM 是带命中计数的 llmStore 假实现，用于断言列表读是否穿透到底层。
type fakeLLM struct {
	providers    []llmcfg.Provider
	providersHit int64 // ListProviders 打底层次数
	routes       []llmcfg.RoleRoute
	routesHit    int64 // ListRoleRoutes 打底层次数
}

func (f *fakeLLM) CreateProvider(_ context.Context, p llmcfg.ProviderParams) (llmcfg.Provider, error) {
	return llmcfg.Provider{Key: p.Key}, nil
}
func (f *fakeLLM) UpdateProvider(_ context.Context, p llmcfg.ProviderParams) (llmcfg.Provider, error) {
	return llmcfg.Provider{Key: p.Key}, nil
}
func (f *fakeLLM) DeleteProvider(_ context.Context, _ string) error { return nil }
func (f *fakeLLM) GetProvider(_ context.Context, key string) (llmcfg.Provider, error) {
	return llmcfg.Provider{Key: key}, nil
}
func (f *fakeLLM) ListProviders(_ context.Context, _ bool) ([]llmcfg.Provider, error) {
	atomic.AddInt64(&f.providersHit, 1)
	return f.providers, nil
}
func (f *fakeLLM) UpsertRoleRoute(_ context.Context, role, providerKey string) (llmcfg.RoleRoute, error) {
	return llmcfg.RoleRoute{Role: role, ProviderKey: providerKey}, nil
}
func (f *fakeLLM) DeleteRoleRoute(_ context.Context, _ string) error { return nil }
func (f *fakeLLM) ListRoleRoutes(_ context.Context) ([]llmcfg.RoleRoute, error) {
	atomic.AddInt64(&f.routesHit, 1)
	return f.routes, nil
}
func (f *fakeLLM) GetRouting(_ context.Context) (llmcfg.Routing, error) {
	return llmcfg.Routing{Roles: map[string]string{}}, nil
}

// newLLMTestStore 用 miniredis + 假底层 store 构造 Store。
func newLLMTestStore(t *testing.T, f *fakeLLM) *Store {
	t.Helper()
	mr := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return newWithStore(f, cachestore.New(rdb, 0))
}

// TestListProviders_CachedAndInvalidated 验证：ListProviders 走缓存（二读不打底层），provider 写后失效。
func TestListProviders_CachedAndInvalidated(t *testing.T) {
	ctx := context.Background()
	f := &fakeLLM{providers: []llmcfg.Provider{{Key: "mimo", Enabled: true}}}
	s := newLLMTestStore(t, f)

	if _, err := s.ListProviders(ctx, false); err != nil {
		t.Fatalf("first list: %v", err)
	}
	if _, err := s.ListProviders(ctx, false); err != nil {
		t.Fatalf("second list: %v", err)
	}
	if got := atomic.LoadInt64(&f.providersHit); got != 1 {
		t.Fatalf("期望列表只打底层 1 次（二读命中缓存），实际 %d", got)
	}
	if !s.cache.L1Has(keyProvidersList(false)) {
		t.Fatal("首读后 providers 列表键应回填 L1")
	}
	// SaveProvider → providerKeys 含两个列表键，应失效。
	if _, err := s.SaveProvider(ctx, llmcfg.ProviderParams{Key: "mimo"}); err != nil {
		t.Fatalf("save provider: %v", err)
	}
	if s.cache.L1Has(keyProvidersList(false)) {
		t.Fatal("providers 列表键应被失效")
	}
	if _, err := s.ListProviders(ctx, false); err != nil {
		t.Fatalf("relist: %v", err)
	}
	if got := atomic.LoadInt64(&f.providersHit); got != 2 {
		t.Fatalf("失效后重读应再打底层 1 次（共 2），实际 %d", got)
	}
}

// TestListProviders_EnabledFlagSeparateKeys 验证：onlyEnabled 分两键，互不串味。
func TestListProviders_EnabledFlagSeparateKeys(t *testing.T) {
	ctx := context.Background()
	f := &fakeLLM{providers: []llmcfg.Provider{{Key: "mimo", Enabled: true}}}
	s := newLLMTestStore(t, f)

	if _, err := s.ListProviders(ctx, false); err != nil {
		t.Fatalf("list all: %v", err)
	}
	if _, err := s.ListProviders(ctx, true); err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	// 两个不同 onlyEnabled 值 = 两个键，各打一次底层。
	if got := atomic.LoadInt64(&f.providersHit); got != 2 {
		t.Fatalf("期望 all/enabled 各打底层 1 次（共 2），实际 %d", got)
	}
	if !s.cache.L1Has(keyProvidersList(true)) || !s.cache.L1Has(keyProvidersList(false)) {
		t.Fatal("两个列表键都应回填 L1")
	}
}

// TestListRoleRoutes_CachedAndInvalidated 验证：ListRoleRoutes 走缓存，路由写后失效（含路由快照哨兵）。
func TestListRoleRoutes_CachedAndInvalidated(t *testing.T) {
	ctx := context.Background()
	f := &fakeLLM{routes: []llmcfg.RoleRoute{{Role: llmcfg.ComplexityMedium, ProviderKey: "mimo"}}}
	s := newLLMTestStore(t, f)

	if _, err := s.ListRoleRoutes(ctx); err != nil {
		t.Fatalf("first list: %v", err)
	}
	if _, err := s.ListRoleRoutes(ctx); err != nil {
		t.Fatalf("second list: %v", err)
	}
	if got := atomic.LoadInt64(&f.routesHit); got != 1 {
		t.Fatalf("期望路由列表只打底层 1 次（二读命中缓存），实际 %d", got)
	}
	// UpsertRoleRoute → routeKeys 含列表键 + 路由快照哨兵，均应失效。
	if _, err := s.UpsertRoleRoute(ctx, llmcfg.ComplexityComplex, "mimo"); err != nil {
		t.Fatalf("upsert route: %v", err)
	}
	if s.cache.L1Has(keyRoleRoutesList) {
		t.Fatal("路由列表键应被失效")
	}
	if _, err := s.ListRoleRoutes(ctx); err != nil {
		t.Fatalf("relist: %v", err)
	}
	if got := atomic.LoadInt64(&f.routesHit); got != 2 {
		t.Fatalf("失效后重读应再打底层 1 次（共 2），实际 %d", got)
	}
}

// TestDeleteRoleRoute_InvalidatesRoutingSnapshot 验证：删路由也失效路由快照哨兵（两跳解析读的是它）。
func TestDeleteRoleRoute_InvalidatesRoutingSnapshot(t *testing.T) {
	ctx := context.Background()
	f := &fakeLLM{routes: []llmcfg.RoleRoute{{Role: llmcfg.ComplexityMedium, ProviderKey: "mimo"}}}
	s := newLLMTestStore(t, f)

	if _, err := s.Routing(ctx); err != nil { // 暖起路由快照哨兵
		t.Fatalf("warm routing: %v", err)
	}
	if !s.cache.L1Has(keyRouting) {
		t.Fatal("Routing 首读后应回填快照哨兵")
	}
	if err := s.DeleteRoleRoute(ctx, llmcfg.ComplexityMedium); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	if s.cache.L1Has(keyRouting) {
		t.Fatal("删路由应失效路由快照哨兵")
	}
}
