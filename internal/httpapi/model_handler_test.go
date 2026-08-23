package httpapi

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/V3teran/liusha/internal/config/llmcfg"
)

// fakeModel 是 ModelAPI 的内存实现，记录调用以断言 handler 编排。
// fkOn 里的方法名（"delete_provider"/"upsert_route"）触发 23503，模拟 FK 约束。
type fakeModel struct {
	providers []llmcfg.Provider
	routes    []llmcfg.RoleRoute

	savedProvider *llmcfg.ProviderParams
	savedRoute    *llmcfg.RoleRoute
	deleted       []string // 记录被删的 key/role

	fkOn   map[string]bool // 方法名 → 是否返回 FK 违反
	getErr error
}

func fkErr() error { return &pgconn.PgError{Code: "23503", Message: "FK violation"} }

func (f *fakeModel) ListProviders(_ context.Context, onlyEnabled bool) ([]llmcfg.Provider, error) {
	if !onlyEnabled {
		return f.providers, nil
	}
	out := make([]llmcfg.Provider, 0, len(f.providers))
	for _, p := range f.providers {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakeModel) ProviderByKey(_ context.Context, key string) (llmcfg.Provider, error) {
	if f.getErr != nil {
		return llmcfg.Provider{}, f.getErr
	}
	for _, p := range f.providers {
		if p.Key == key {
			return p, nil
		}
	}
	return llmcfg.Provider{}, pgxErrNoRows()
}
func (f *fakeModel) SaveProvider(_ context.Context, p llmcfg.ProviderParams) (llmcfg.Provider, error) {
	f.savedProvider = &p
	return llmcfg.Provider{
		Key: p.Key, Type: p.Type, BaseURL: p.BaseURL, DefaultModel: p.DefaultModel,
		EncryptedAPIKey: p.EncryptedAPIKey, MaxTokens: p.MaxTokens, SupportsTools: p.SupportsTools,
		SupportsVision: p.SupportsVision, ContextWindow: p.ContextWindow,
		Description: p.Description, SortOrder: p.SortOrder, Enabled: p.Enabled,
	}, nil
}
func (f *fakeModel) DeleteProvider(_ context.Context, key string) error {
	if f.fkOn["delete_provider"] {
		return fkErr()
	}
	f.deleted = append(f.deleted, key)
	return nil
}
func (f *fakeModel) ListRoleRoutes(_ context.Context) ([]llmcfg.RoleRoute, error) {
	return f.routes, nil
}
func (f *fakeModel) UpsertRoleRoute(_ context.Context, role, providerKey string) (llmcfg.RoleRoute, error) {
	if f.fkOn["upsert_route"] {
		return llmcfg.RoleRoute{}, fkErr()
	}
	rr := llmcfg.RoleRoute{Role: role, ProviderKey: providerKey}
	f.savedRoute = &rr
	return rr, nil
}
func (f *fakeModel) DeleteRoleRoute(_ context.Context, role string) error {
	f.deleted = append(f.deleted, role)
	return nil
}

// fakeEncrypter 是 KeyEncrypter 的测试替身：不做真加密，只加前缀方便断言「确实经过加密路径」。
type fakeEncrypter struct{}

func (fakeEncrypter) Encrypt(plaintext string) ([]byte, error) {
	return []byte("sealed:" + plaintext), nil
}

// TestListProviders_ReturnsAllWithKeyPresent：GET /models/providers 全量含 disabled，
// 每条附 key_present（是否已有可用密钥来源）但绝不含密钥值本身（明文或密文）。
func TestListProviders_ReturnsAllWithKeyPresent(t *testing.T) {
	fm := &fakeModel{providers: []llmcfg.Provider{
		{Key: "deepseek", Type: "openai_compat", BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat", EncryptedAPIKey: []byte("sealed:x"), ContextWindow: 64000, Enabled: true},
		{Key: "qwen", Type: "openai_compat", BaseURL: "https://x", DefaultModel: "qwen-max", ContextWindow: 32000, Enabled: false},
	}}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/models/providers", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	arr, _ := body["providers"].([]any)
	if len(arr) != 2 {
		t.Fatalf("应返回全量含 disabled，got %d", len(arr))
	}
	first, _ := arr[0].(map[string]any)
	for _, k := range []string{"key", "type", "base_url", "default_model", "key_present", "context_window", "enabled"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("缺字段 %q: %v", k, first)
		}
	}
	if first["key_present"] != true {
		t.Fatalf("deepseek 已加密存密钥应 key_present=true: %v", first)
	}
	// 铁律：响应绝不含密钥值本身（无论明文还是密文）
	if _, hasKey := first["api_key"]; hasKey {
		t.Fatalf("响应不应含 api_key 字段: %v", first)
	}
	if _, hasEnc := first["encrypted_api_key"]; hasEnc {
		t.Fatalf("响应不应含 encrypted_api_key 字段: %v", first)
	}
	second, _ := arr[1].(map[string]any)
	if second["key_present"] != false {
		t.Fatalf("qwen 无密钥来源应 key_present=false: %v", second)
	}
}

// TestSaveProvider_ValidationRejects：缺关键字段或 type 非法 → 400 中文。
func TestSaveProvider_ValidationRejects(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}, KeyEncrypter: fakeEncrypter{}})
	defer srv.Close()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"缺 key", map[string]any{"type": "openai_compat", "base_url": "u", "default_model": "m", "api_key": "k", "context_window": 1}},
		{"type 非法", map[string]any{"key": "k", "type": "foo", "base_url": "u", "default_model": "m", "api_key": "k", "context_window": 1}},
		{"缺 base_url", map[string]any{"key": "k", "type": "openai_compat", "default_model": "m", "api_key": "k", "context_window": 1}},
		{"新建缺 api_key", map[string]any{"key": "k", "type": "openai_compat", "base_url": "u", "default_model": "m", "context_window": 1}},
		{"context_window<=0", map[string]any{"key": "k", "type": "openai_compat", "base_url": "u", "default_model": "m", "api_key": "k", "context_window": 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, "POST", srv.URL+"/models/providers", tc.body)
			if code != 400 {
				t.Fatalf("want 400, got %d (%v)", code, body)
			}
			if _, ok := body["error"].(string); !ok {
				t.Fatalf("缺中文 error 字段: %v", body)
			}
		})
	}
}

// TestSaveProvider_WiresParams：合法 POST → SaveProvider 收到加密后的密文（非明文）。
func TestSaveProvider_WiresParams(t *testing.T) {
	fm := &fakeModel{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/models/providers", map[string]any{
		"key": "anthropic", "type": "anthropic", "base_url": "https://api.anthropic.com",
		"default_model": "claude", "api_key": "sk-real-secret", "context_window": 200000,
		"supports_vision": true, "enabled": true,
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fm.savedProvider == nil {
		t.Fatal("SaveProvider 未被调用")
	}
	if fm.savedProvider.Key != "anthropic" || !fm.savedProvider.SupportsVision {
		t.Fatalf("参数未透传: %+v", fm.savedProvider)
	}
	if string(fm.savedProvider.EncryptedAPIKey) != "sealed:sk-real-secret" {
		t.Fatalf("api_key 应经加密器处理，不应是明文: %v", fm.savedProvider.EncryptedAPIKey)
	}
}

// TestSaveProvider_EditWithoutAPIKeyKeepsExisting：PUT 不带 api_key → KeepExistingKey，不清空已存密钥。
func TestSaveProvider_EditWithoutAPIKeyKeepsExisting(t *testing.T) {
	fm := &fakeModel{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/models/providers/deepseek", map[string]any{
		"key": "deepseek", "type": "openai_compat", "base_url": "https://api.deepseek.com",
		"default_model": "deepseek-chat", "context_window": 64000, "enabled": true,
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fm.savedProvider == nil || !fm.savedProvider.KeepExistingKey {
		t.Fatalf("未重填 api_key 应设 KeepExistingKey: %+v", fm.savedProvider)
	}
}

// TestSaveProvider_NoEncrypterMeansRouteNotRegistered：缺 KeyEncrypter 时写路径不注册（404），
// fail-closed——不能让前端明文 API Key 落到一个不会加密的路径。
func TestSaveProvider_NoEncrypterMeansRouteNotRegistered(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}}) // 无 KeyEncrypter
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/models/providers", map[string]any{
		"key": "x", "type": "openai_compat", "base_url": "u", "default_model": "m", "api_key": "k", "context_window": 1,
	})
	if code != 404 {
		t.Fatalf("缺加密器时写路径应不注册（404），got %d", code)
	}
}

// TestDeleteProvider_RestrictConflict：被角色路由 FK 引用 → 409 中文。
func TestDeleteProvider_RestrictConflict(t *testing.T) {
	fm := &fakeModel{fkOn: map[string]bool{"delete_provider": true}}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, body := doJSON(t, "DELETE", srv.URL+"/models/providers/deepseek", nil)
	if code != 409 {
		t.Fatalf("want 409, got %d (%v)", code, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("缺中文 error 字段: %v", body)
	}
}

// TestListRouting_ReturnsRoutes：GET /models/routing 返回角色→provider 路由全景（无别名层）。
func TestListRouting_ReturnsRoutes(t *testing.T) {
	fm := &fakeModel{
		routes: []llmcfg.RoleRoute{
			{Role: llmcfg.TierVision, ProviderKey: "deepseek"},
			{Role: llmcfg.TierHeavy, ProviderKey: "deepseek"},
		},
	}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/models/routing", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if _, hasAliases := body["aliases"]; hasAliases {
		t.Fatalf("别名层已废弃，响应不应含 aliases: %v", body)
	}
	if r, _ := body["routes"].([]any); len(r) != 2 {
		t.Fatalf("want 2 routes, got %v", body["routes"])
	}
}

// TestSaveRoleRoute_FKConflict：provider_key 不存在撞 FK → 409。
func TestSaveRoleRoute_FKConflict(t *testing.T) {
	fm := &fakeModel{fkOn: map[string]bool{"upsert_route": true}}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/models/routes/planner", map[string]any{"provider_key": "ghost"})
	if code != 409 {
		t.Fatalf("want 409, got %d", code)
	}
}

// TestSaveRoleRoute_Wires：合法 PUT → UpsertRoleRoute 收到 role(路径)+provider_key(体)。
func TestSaveRoleRoute_Wires(t *testing.T) {
	fm := &fakeModel{}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/models/routes/planner", map[string]any{"provider_key": "deepseek"})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fm.savedRoute == nil || fm.savedRoute.Role != "planner" || fm.savedRoute.ProviderKey != "deepseek" {
		t.Fatalf("路由未透传: %+v", fm.savedRoute)
	}
}

// TestSaveRoleRoute_MissingProviderKey：缺 provider_key → 400。
func TestSaveRoleRoute_MissingProviderKey(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/models/routes/planner", map[string]any{})
	if code != 400 {
		t.Fatalf("want 400, got %d", code)
	}
}

// TestDeleteRoleRoute_OK：DELETE /models/routes/:role → 200，记录删除。
func TestDeleteRoleRoute_OK(t *testing.T) {
	fm := &fakeModel{}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, _ := doJSON(t, "DELETE", srv.URL+"/models/routes/planner", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if len(fm.deleted) != 1 || fm.deleted[0] != "planner" {
		t.Fatalf("未记录删除: %v", fm.deleted)
	}
}
