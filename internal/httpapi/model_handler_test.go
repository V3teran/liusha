package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/V3teran/liusha/internal/llmconfig"
)

// pgxErrNoRows 复用底层 store 的 not-found 语义供 mock 返回。
func pgxErrNoRows() error { return pgx.ErrNoRows }

// fakeModel 是 ModelAPI 的内存实现，记录调用以断言 handler 编排。
// fkOn 里的方法名（"delete_provider"/"upsert_route"）触发 23503，模拟 FK 约束。
type fakeModel struct {
	providers []llmconfig.Provider
	routes    []llmconfig.RoleRoute

	savedProvider *llmconfig.ProviderParams
	savedRoute    *llmconfig.RoleRoute
	deleted       []string // 记录被删的 key/role

	fkOn          map[string]bool // 方法名 → 是否返回 FK 违反
	getErr        error
	updateMissing bool  // UpdateProvider 模拟目标不存在
	deleteMissing bool  // DeleteProvider 模拟目标不存在
	clearErr      error // ClearProviderKey 的返回错误
	routeErr      error // UpsertRoleRoute 的返回错误
}

func fkErr() error { return &pgconn.PgError{Code: "23503", Message: "FK violation"} }

func (f *fakeModel) ListProviders(_ context.Context, onlyEnabled bool) ([]llmconfig.Provider, error) {
	if !onlyEnabled {
		return f.providers, nil
	}
	out := make([]llmconfig.Provider, 0, len(f.providers))
	for _, p := range f.providers {
		if p.Enabled {
			out = append(out, p)
		}
	}
	return out, nil
}
func (f *fakeModel) ProviderByCode(_ context.Context, key string) (llmconfig.Provider, error) {
	if f.getErr != nil {
		return llmconfig.Provider{}, f.getErr
	}
	for _, p := range f.providers {
		if p.Code == key {
			return p, nil
		}
	}
	return llmconfig.Provider{}, pgxErrNoRows()
}
func (f *fakeModel) CreateProvider(_ context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error) {
	for _, existing := range f.providers {
		if existing.Code == p.Code {
			return llmconfig.Provider{}, fmt.Errorf("provider %q 已存在: %w", p.Code, llmconfig.ErrConflict)
		}
	}
	f.savedProvider = &p
	return providerFromParams(p), nil
}
func (f *fakeModel) UpdateProvider(_ context.Context, p llmconfig.ProviderParams) (llmconfig.Provider, error) {
	if f.updateMissing {
		return llmconfig.Provider{}, fmt.Errorf("update provider %q: %w", p.Code, pgxErrNoRows())
	}
	f.savedProvider = &p
	return providerFromParams(p), nil
}
func providerFromParams(p llmconfig.ProviderParams) llmconfig.Provider {
	return llmconfig.Provider{
		Code: p.Code, Type: p.Type, BaseURL: p.BaseURL, DefaultModel: p.DefaultModel,
		EncryptedAPIKey: p.EncryptedAPIKey, MaxTokens: p.MaxTokens, ThinkingBudgetTokens: p.ThinkingBudgetTokens,
		SupportsTools: p.SupportsTools, SupportsVision: p.SupportsVision, ContextWindow: p.ContextWindow,
		Description: p.Description, SortOrder: p.SortOrder, Enabled: p.Enabled,
	}
}
func (f *fakeModel) DeleteProvider(_ context.Context, key string) error {
	if f.fkOn["delete_provider"] {
		return fkErr()
	}
	if f.deleteMissing {
		return fmt.Errorf("delete provider %q: %w", key, pgxErrNoRows())
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func (f *fakeModel) ClearProviderKey(_ context.Context, code string) (llmconfig.Provider, error) {
	if f.clearErr != nil {
		return llmconfig.Provider{}, f.clearErr
	}
	f.deleted = append(f.deleted, "key:"+code)
	return llmconfig.Provider{Code: code, APIKeyEnv: "GLM_API_KEY"}, nil
}
func (f *fakeModel) ListRoleRoutes(_ context.Context) ([]llmconfig.RoleRoute, error) {
	return f.routes, nil
}
func (f *fakeModel) UpsertRoleRoute(_ context.Context, role, providerCode string) (llmconfig.RoleRoute, error) {
	if f.fkOn["upsert_route"] {
		return llmconfig.RoleRoute{}, fkErr()
	}
	if f.routeErr != nil {
		return llmconfig.RoleRoute{}, f.routeErr
	}
	rr := llmconfig.RoleRoute{Role: role, ProviderCode: providerCode}
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
	fm := &fakeModel{providers: []llmconfig.Provider{
		{Code: "deepseek", Type: "openai_compat", BaseURL: "https://api.deepseek.com", DefaultModel: "deepseek-chat", EncryptedAPIKey: []byte("sealed:x"), ContextWindow: 64000, Enabled: true},
		{Code: "qwen", Type: "openai_compat", BaseURL: "https://x", DefaultModel: "qwen-max", ContextWindow: 32000, Enabled: false},
	}}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/models/providers", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	arr, _ := body["providers"].([]any)
	if len(arr) != 2 {
		t.Fatalf("应返回全量含 disabled，got %d", len(arr))
	}
	first, _ := arr[0].(map[string]any)
	for _, k := range []string{"code", "type", "base_url", "default_model", "key_present", "context_window", "enabled"} {
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
		{"缺显示名称", map[string]any{"type": "openai_compat", "base_url": "https://x", "default_model": "m", "api_key": "k", "context_window": 1}},
		{"type 非法", map[string]any{"type": "foo", "base_url": "https://x", "display_name": "n", "default_model": "m", "api_key": "k", "context_window": 1}},
		{"缺 base_url", map[string]any{"type": "openai_compat", "display_name": "n", "default_model": "m", "api_key": "k", "context_window": 1}},
		{"base_url 非完整地址", map[string]any{"type": "openai_compat", "base_url": "u", "display_name": "n", "default_model": "m", "api_key": "k", "context_window": 1}},
		{"新建缺 api_key", map[string]any{"type": "openai_compat", "base_url": "https://x", "display_name": "n", "default_model": "m", "context_window": 1}},
		{"context_window<=0", map[string]any{"type": "openai_compat", "base_url": "https://x", "display_name": "n", "default_model": "m", "api_key": "k", "context_window": 0}},
		{"思考预算用于非 anthropic", map[string]any{"type": "openai_compat", "base_url": "https://x", "display_name": "n", "default_model": "m", "api_key": "k", "context_window": 1, "thinking_budget_tokens": 2048}},
		{"思考预算过小", map[string]any{"type": "anthropic", "base_url": "https://x", "display_name": "n", "default_model": "m", "api_key": "k", "context_window": 1, "max_tokens": 8192, "thinking_budget_tokens": 512}},
		{"思考预算不小于 max_tokens", map[string]any{"type": "anthropic", "base_url": "https://x", "display_name": "n", "default_model": "m", "api_key": "k", "context_window": 1, "max_tokens": 4096, "thinking_budget_tokens": 4096}},
		{"思考预算不小于默认 max_tokens", map[string]any{"type": "anthropic", "base_url": "https://x", "display_name": "n", "default_model": "m", "api_key": "k", "context_window": 1, "thinking_budget_tokens": 4096}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, "POST", srv.URL+"/api/models/providers", tc.body)
			if code != 400 {
				t.Fatalf("want 400, got %d (%v)", code, body)
			}
			if _, ok := body["error"].(string); !ok {
				t.Fatalf("缺中文 error 字段: %v", body)
			}
		})
	}
}

// TestSaveProvider_WiresParams：合法 POST → CreateProvider 收到加密后的密文（非明文）与思考预算。
func TestSaveProvider_WiresParams(t *testing.T) {
	fm := &fakeModel{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/api/models/providers", map[string]any{
		"code": "anthropic", "type": "anthropic", "base_url": "https://api.anthropic.com",
		"display_name": "n", "default_model": "claude", "api_key": "sk-real-secret", "context_window": 200000,
		"supports_vision": true, "enabled": true, "max_tokens": 8192, "thinking_budget_tokens": 4096,
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fm.savedProvider == nil {
		t.Fatal("CreateProvider 未被调用")
	}
	if fm.savedProvider.Code != "anthropic" || !fm.savedProvider.SupportsVision || fm.savedProvider.ThinkingBudgetTokens != 4096 {
		t.Fatalf("参数未透传: %+v", fm.savedProvider)
	}
	if string(fm.savedProvider.EncryptedAPIKey) != "sealed:sk-real-secret" {
		t.Fatalf("api_key 应经加密器处理，不应是明文: %v", fm.savedProvider.EncryptedAPIKey)
	}
}

// TestSaveProvider_GeneratesCode：POST 不带 code → 服务端生成 UUID 标识符。
func TestSaveProvider_GeneratesCode(t *testing.T) {
	fm := &fakeModel{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/api/models/providers", map[string]any{
		"type": "openai_compat", "base_url": "https://x", "display_name": "n", "default_model": "m",
		"api_key": "k", "context_window": 1,
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fm.savedProvider == nil {
		t.Fatal("CreateProvider 未被调用")
	}
	if len(fm.savedProvider.Code) < 32 {
		t.Fatalf("code 应为服务端生成的 UUID: %q", fm.savedProvider.Code)
	}
}

// TestSaveProvider_EditWithoutAPIKeyKeepsExisting：PUT 不带 api_key → KeepExistingKey，不清空已存密钥。
func TestSaveProvider_EditWithoutAPIKeyKeepsExisting(t *testing.T) {
	fm := &fakeModel{}
	srv := newTestServer(t, Deps{Models: fm, KeyEncrypter: fakeEncrypter{}})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/models/providers/deepseek", map[string]any{
		"type": "openai_compat", "base_url": "https://api.deepseek.com",
		"display_name": "n", "default_model": "deepseek-chat", "context_window": 64000, "enabled": true,
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fm.savedProvider == nil || !fm.savedProvider.KeepExistingKey || fm.savedProvider.Code != "deepseek" {
		t.Fatalf("未重填 api_key 应设 KeepExistingKey: %+v", fm.savedProvider)
	}
}

// TestSaveProvider_NoEncrypterMeansRouteNotRegistered：缺 KeyEncrypter 时写路径不注册（404），
// fail-closed——不能让前端明文 API Code 落到一个不会加密的路径。
func TestSaveProvider_NoEncrypterMeansRouteNotRegistered(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}}) // 无 KeyEncrypter
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/api/models/providers", map[string]any{
		"type": "openai_compat", "base_url": "https://x", "display_name": "n", "default_model": "m", "api_key": "k", "context_window": 1,
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

	code, body := doJSON(t, "DELETE", srv.URL+"/api/models/providers/deepseek", nil)
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
		routes: []llmconfig.RoleRoute{
			{Role: "vision", ProviderCode: "deepseek"},
			{Role: "heavy", ProviderCode: "deepseek"},
		},
	}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/models/routing", nil)
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

// TestSaveRoleRoute_FKConflict：provider_code 不存在撞 FK → 409。
func TestSaveRoleRoute_FKConflict(t *testing.T) {
	fm := &fakeModel{fkOn: map[string]bool{"upsert_route": true}}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/models/routes/complex", map[string]any{"provider_code": "ghost"})
	if code != 409 {
		t.Fatalf("want 409, got %d", code)
	}
}

// TestSaveRoleRoute_Wires：合法 PUT → UpsertRoleRoute 收到 role(路径)+provider_code(体)。
func TestSaveRoleRoute_Wires(t *testing.T) {
	fm := &fakeModel{}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/models/routes/complex", map[string]any{"provider_code": "deepseek"})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fm.savedRoute == nil || fm.savedRoute.Role != "complex" || fm.savedRoute.ProviderCode != "deepseek" {
		t.Fatalf("路由未透传: %+v", fm.savedRoute)
	}
}

// TestSaveRoleRoute_MissingProviderKey：缺 provider_key → 400。
func TestSaveRoleRoute_MissingProviderKey(t *testing.T) {
	srv := newTestServer(t, Deps{Models: &fakeModel{}})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/models/routes/complex", map[string]any{})
	if code != 400 {
		t.Fatalf("want 400, got %d", code)
	}
}

// TestDeleteRoleRoute_OK：DELETE /models/routes/:role → 200，记录删除。
func TestDeleteRoleRoute_OK(t *testing.T) {
	fm := &fakeModel{}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, _ := doJSON(t, "DELETE", srv.URL+"/api/models/routes/complex", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if len(fm.deleted) != 1 || fm.deleted[0] != "complex" {
		t.Fatalf("未记录删除: %v", fm.deleted)
	}
}

// TestProviderWriteSemantics：POST 只新建（重复 code → 409）；PUT 只改路径指向的
// provider（不存在 → 404、请求体 code 与路径不一致 → 400）。
func TestProviderWriteSemantics(t *testing.T) {
	valid := map[string]any{"type": "openai_compat", "base_url": "https://x", "display_name": "n", "default_model": "m", "context_window": 1}
	with := func(extra map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range valid {
			out[k] = v
		}
		for k, v := range extra {
			out[k] = v
		}
		return out
	}
	cases := []struct {
		name   string
		fm     *fakeModel
		method string
		path   string
		body   map[string]any
		want   int
	}{
		{"POST 重复 code", &fakeModel{providers: []llmconfig.Provider{{Code: "dup"}}}, "POST", "/api/models/providers", with(map[string]any{"code": "dup", "api_key": "k"}), 409},
		{"PUT 不存在", &fakeModel{updateMissing: true}, "PUT", "/api/models/providers/ghost", valid, 404},
		{"PUT 体内 code 与路径不一致", &fakeModel{}, "PUT", "/api/models/providers/a", with(map[string]any{"code": "b"}), 400},
		{"PUT 只带路径 code", &fakeModel{}, "PUT", "/api/models/providers/a", valid, 200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, Deps{Models: tc.fm, KeyEncrypter: fakeEncrypter{}})
			defer srv.Close()
			code, body := doJSON(t, tc.method, srv.URL+tc.path, tc.body)
			if code != tc.want {
				t.Fatalf("want %d, got %d (%v)", tc.want, code, body)
			}
		})
	}
}

// TestProviderNotFoundAndConflict：读/删/清密钥的「不存在」统一 404 中文；无环境变量
// 可回落时清除密钥 → 409。
func TestProviderNotFoundAndConflict(t *testing.T) {
	cases := []struct {
		name   string
		fm     *fakeModel
		method string
		path   string
		want   int
	}{
		{"GET 不存在", &fakeModel{}, "GET", "/api/models/providers/ghost", 404},
		{"DELETE 不存在", &fakeModel{deleteMissing: true}, "DELETE", "/api/models/providers/ghost", 404},
		{"清密钥 不存在", &fakeModel{clearErr: fmt.Errorf("get provider: %w", pgxErrNoRows())}, "DELETE", "/api/models/providers/ghost/key", 404},
		{"清密钥 无环境变量", &fakeModel{clearErr: fmt.Errorf("无可回落的来源: %w", llmconfig.ErrConflict)}, "DELETE", "/api/models/providers/a/key", 409},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newTestServer(t, Deps{Models: tc.fm})
			defer srv.Close()
			code, body := doJSON(t, tc.method, srv.URL+tc.path, nil)
			if code != tc.want {
				t.Fatalf("want %d, got %d (%v)", tc.want, code, body)
			}
			if msg, _ := body["error"].(string); msg == "" || strings.Contains(msg, "no rows") {
				t.Fatalf("错误消息应为中文且不外透驱动原文: %v", body)
			}
		})
	}
}

// TestSaveRoleRoute_InvalidTier：档名不是三档之一 → 400（store 校验经 respondErr 收敛）。
func TestSaveRoleRoute_InvalidTier(t *testing.T) {
	fm := &fakeModel{routeErr: fmt.Errorf("非法档位: %w", llmconfig.ErrInvalid)}
	srv := newTestServer(t, Deps{Models: fm})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/models/routes/whatever", map[string]any{"provider_code": "deepseek"})
	if code != 400 {
		t.Fatalf("want 400, got %d", code)
	}
}

// TestProviderJSON_KeyPresentFollowsResolvability：key_present 反映能否真正解析出密钥——
// 只填了环境变量名而变量为空时为 false；思考预算随响应返回。
func TestProviderJSON_KeyPresentFollowsResolvability(t *testing.T) {
	t.Setenv("LIUSHA_TEST_EMPTY_KEY", "")
	t.Setenv("LIUSHA_TEST_SET_KEY", "sk-x")
	got := providerJSON(llmconfig.Provider{APIKeyEnv: "LIUSHA_TEST_EMPTY_KEY"})
	if got["key_present"] != false {
		t.Fatalf("环境变量为空应 key_present=false: %v", got)
	}
	got = providerJSON(llmconfig.Provider{APIKeyEnv: "LIUSHA_TEST_SET_KEY", ThinkingBudgetTokens: 2048})
	if got["key_present"] != true || got["thinking_budget_tokens"] != int64(2048) {
		t.Fatalf("环境变量有值应 key_present=true 且带思考预算: %v", got)
	}
}

// doJSON 发一条带鉴权的请求，返回状态码 + 解码后的 body。
func doJSON(t *testing.T, method, url string, body any) (int, map[string]any) {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, _ := http.NewRequest(method, url, rdr)
	req.AddCookie(testSessionCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}
