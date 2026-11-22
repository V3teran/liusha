package llmstore

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/V3teran/liusha/internal/cachestore"
	"github.com/V3teran/liusha/internal/llmconfig"
)

// fakeLLM 是带命中计数的 llmStore 假实现，用于断言列表读是否穿透到底层。
type fakeLLM struct {
	providers    []llmconfig.Provider
	providersHit int64 // ListProviders 打底层次数
	routes       []llmconfig.RoleRoute
	routesHit    int64 // ListRoleRoutes 打底层次数
	retrievalRow llmconfig.RetrievalModel
	retrievalHit int64 // GetRetrievalModel 打底层次数
}

func (f *fakeLLM) CreateProvider(_ context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error) {
	return llmconfig.Provider{Code: p.Code}, nil
}
func (f *fakeLLM) UpdateProvider(_ context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error) {
	return llmconfig.Provider{Code: p.Code}, nil
}
func (f *fakeLLM) DeleteProvider(_ context.Context, _ string) error { return nil }

func (f *fakeLLM) GetProvider(_ context.Context, key string) (llmconfig.Provider, error) {
	return llmconfig.Provider{Code: key}, nil
}
func (f *fakeLLM) ListProviders(_ context.Context, _ bool) ([]llmconfig.Provider, error) {
	atomic.AddInt64(&f.providersHit, 1)
	return f.providers, nil
}
func (f *fakeLLM) UpsertRoleRoute(_ context.Context, role, providerCode string) (llmconfig.RoleRoute, error) {
	return llmconfig.RoleRoute{Role: role, ProviderCode: providerCode}, nil
}
func (f *fakeLLM) DeleteRoleRoute(_ context.Context, _ string) error { return nil }
func (f *fakeLLM) ListRoleRoutes(_ context.Context) ([]llmconfig.RoleRoute, error) {
	atomic.AddInt64(&f.routesHit, 1)
	return f.routes, nil
}
func (f *fakeLLM) GetRouting(_ context.Context) (llmconfig.Routing, error) {
	return llmconfig.Routing{Roles: map[string]string{}}, nil
}

func (f *fakeLLM) GetRetrievalModel(context.Context) (llmconfig.RetrievalModel, error) {
	atomic.AddInt64(&f.retrievalHit, 1)
	return f.retrievalRow, nil
}
func (f *fakeLLM) UpsertRetrievalModel(_ context.Context, p llmconfig.RetrievalParams) (llmconfig.RetrievalModel, error) {
	f.retrievalRow = llmconfig.RetrievalModel{
		BaseURL: p.BaseURL, EmbeddingModel: p.EmbeddingModel, RerankModel: p.RerankModel,
		EncryptedAPIKey: p.EncryptedAPIKey, APIKeyLast4: p.APIKeyLast4,
	}
	return f.retrievalRow, nil
}
func (f *fakeLLM) ClearRetrievalKey(context.Context) error {
	f.retrievalRow.EncryptedAPIKey = nil
	f.retrievalRow.APIKeyLast4 = ""
	return nil
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
	f := &fakeLLM{providers: []llmconfig.Provider{{Code: "glm", Enabled: true}}}
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
	// UpdateProvider → providerKeys 含两个列表键，应失效。
	if _, err := s.UpdateProvider(ctx, llmconfig.ProviderParams{Code: "glm"}); err != nil {
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
	f := &fakeLLM{providers: []llmconfig.Provider{{Code: "glm", Enabled: true}}}
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
	f := &fakeLLM{routes: []llmconfig.RoleRoute{{Role: llmconfig.ComplexityMedium, ProviderCode: "glm"}}}
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
	if _, err := s.UpsertRoleRoute(ctx, llmconfig.ComplexityComplex, "glm"); err != nil {
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
	f := &fakeLLM{routes: []llmconfig.RoleRoute{{Role: llmconfig.ComplexityMedium, ProviderCode: "glm"}}}
	s := newLLMTestStore(t, f)

	if _, err := s.Routing(ctx); err != nil { // 暖起路由快照哨兵
		t.Fatalf("warm routing: %v", err)
	}
	if !s.cache.L1Has(keyRouting) {
		t.Fatal("Routing 首读后应回填快照哨兵")
	}
	if err := s.DeleteRoleRoute(ctx, llmconfig.ComplexityMedium); err != nil {
		t.Fatalf("delete route: %v", err)
	}
	if s.cache.L1Has(keyRouting) {
		t.Fatal("删路由应失效路由快照哨兵")
	}
}

// TestRetrievalModel_CachedAndInvalidated 验证：检索模型密钥走缓存（二读不打底层），
// 写后失效——本进程下次读即新值（跨进程靠广播，与 provider 同机制）。
func TestRetrievalModel_CachedAndInvalidated(t *testing.T) {
	f := &fakeLLM{}
	st := newLLMTestStore(t, f)
	ctx := context.Background()

	if _, err := st.RetrievalModel(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := st.RetrievalModel(ctx); err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt64(&f.retrievalHit); got != 1 {
		t.Fatalf("第二次读应命中缓存，底层读了 %d 次", got)
	}

	saved, err := st.SetRetrievalModel(ctx, llmconfig.RetrievalParams{
		BaseURL: "https://gw.example.com", EmbeddingModel: "embed-x", RerankModel: "rerank-x",
		EncryptedAPIKey: []byte("sealed"), APIKeyLast4: "abcd",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.APIKeyLast4 != "abcd" || saved.EmbeddingModel != "embed-x" {
		t.Fatalf("保存应回读整行: %+v", saved)
	}
	row, err := st.RetrievalModel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !row.HasKey() || row.APIKeyLast4 != "abcd" || row.BaseURL != "https://gw.example.com" {
		t.Fatalf("写后应读到新值: %+v", row)
	}

	if err := st.ClearRetrievalKey(ctx); err != nil {
		t.Fatal(err)
	}
	row, err = st.RetrievalModel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if row.HasKey() {
		t.Fatalf("清密钥后应无密钥: %+v", row)
	}
	if row.BaseURL != "https://gw.example.com" {
		t.Fatalf("清密钥应保留地址自定义: %+v", row)
	}
}
