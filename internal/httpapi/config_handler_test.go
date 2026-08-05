package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

// fakeConfig 是 ConfigAPI 的内存实现，记录调用以断言 handler 编排（playbook 层已废）。
type fakeConfig struct {
	scenarios []cfgscenario.Scenario
	hunters   map[string]cfghunter.Hunter // by id

	savedScenario *cfgscenario.NewParams
	savedHunter   *cfghunter.NewParams
	deleteHunter  error // DeleteHunter 返回的错误（模拟 FK RESTRICT）
}

func (f *fakeConfig) ListScenarios(_ context.Context, onlyEnabled bool) ([]cfgscenario.Scenario, error) {
	if !onlyEnabled {
		return f.scenarios, nil
	}
	out := make([]cfgscenario.Scenario, 0, len(f.scenarios))
	for _, s := range f.scenarios {
		if s.Enabled {
			out = append(out, s)
		}
	}
	return out, nil
}
func (f *fakeConfig) ScenarioByID(_ context.Context, id string) (cfgscenario.Scenario, error) {
	for _, s := range f.scenarios {
		if s.ID == id {
			return s, nil
		}
	}
	return cfgscenario.Scenario{}, pgxErrNoRows()
}
func (f *fakeConfig) SaveScenario(_ context.Context, p cfgscenario.NewParams) (cfgscenario.Scenario, error) {
	f.savedScenario = &p
	return cfgscenario.Scenario{ID: "sc-new", Code: p.Code, Name: p.Name, Engine: p.Engine, SoloHunterID: p.SoloHunterID}, nil
}
func (f *fakeConfig) DeleteScenario(_ context.Context, _, _ string) error { return nil }

func (f *fakeConfig) ListHunters(_ context.Context, _ bool) ([]cfghunter.Hunter, error) {
	out := make([]cfghunter.Hunter, 0, len(f.hunters))
	for _, h := range f.hunters {
		out = append(out, h)
	}
	return out, nil
}
func (f *fakeConfig) HunterByID(_ context.Context, id string) (cfghunter.Hunter, error) {
	if h, ok := f.hunters[id]; ok {
		return h, nil
	}
	return cfghunter.Hunter{}, pgxErrNoRows()
}
func (f *fakeConfig) SaveHunter(_ context.Context, p cfghunter.NewParams) (cfghunter.Hunter, error) {
	f.savedHunter = &p
	return cfghunter.Hunter{ID: "h-new", Code: p.Code, Kind: p.Kind, Name: p.Name, CliTools: p.CliTools}, nil
}
func (f *fakeConfig) DeleteHunter(_ context.Context, _, _ string) error { return f.deleteHunter }

// fakeTooling 是 ToolingAPI 的内存实现，供 /tooling/tools 测试。
type fakeTooling struct{ byCat map[string][]manifest.Tool }

func (f *fakeTooling) ByCategory() map[string][]manifest.Tool { return f.byCat }

// pgxErrNoRows 复用底层 store 的 not-found 语义供 mock 返回。
func pgxErrNoRows() error { return pgx.ErrNoRows }

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
	req.Header.Set("X-API-Key", "k")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&out)
	return resp.StatusCode, out
}

// TestListScenarios_ReturnsAllWithFullFields：GET /scenarios 单一口径返回全量（含
// disabled）+ 全字段（含 instruction/engine/solo_hunter_id/enabled）。picker 与配置页
// 共用此响应，可见 ≠ 可用交由前端按 enabled 区分（无 ?all 分流）。
func TestListScenarios_ReturnsAllWithFullFields(t *testing.T) {
	solo := "h-recon"
	fc := &fakeConfig{scenarios: []cfgscenario.Scenario{
		{ID: "s1", Code: "on", Name: "启用", Instruction: "I1", Engine: "swarm", Enabled: true},
		{ID: "s2", Code: "off", Name: "停用", Instruction: "I2", Engine: "solo", SoloHunterID: &solo, Enabled: false},
	}}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/scenarios", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	arr, _ := body["scenarios"].([]any)
	if len(arr) != 2 {
		t.Fatalf("应返回全量含 disabled，got %d", len(arr))
	}
	first, _ := arr[0].(map[string]any)
	for _, k := range []string{"id", "code", "name", "description", "instruction", "engine", "solo_hunter_id", "enabled"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("缺全字段 %q: %v", k, first)
		}
	}
}

// TestPostScenario_SoloRequiresHunter：solo 引擎缺 solo_hunter_id → 400 中文。
func TestPostScenario_SoloRequiresHunter(t *testing.T) {
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}})
	defer srv.Close()

	code, body := doJSON(t, "POST", srv.URL+"/scenarios", map[string]any{
		"code": "c", "name": "n", "engine": "solo",
	})
	if code != 400 {
		t.Fatalf("want 400, got %d (%v)", code, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("缺中文 error 字段: %v", body)
	}
}

// TestPostScenario_SwarmRejectsHunter：swarm 引擎带 solo_hunter_id → 400 中文
// （子代理池=全部 enabled 领域猎手，不接受单点指定）。
func TestPostScenario_SwarmRejectsHunter(t *testing.T) {
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}})
	defer srv.Close()

	code, body := doJSON(t, "POST", srv.URL+"/scenarios", map[string]any{
		"code": "c", "name": "n", "engine": "swarm", "solo_hunter_id": "h-recon",
	})
	if code != 400 {
		t.Fatalf("want 400, got %d (%v)", code, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("缺中文 error 字段: %v", body)
	}
}

// TestPostScenario_SoloWiresHunter：solo + solo_hunter_id → SaveScenario 收到 *string。
func TestPostScenario_SoloWiresHunter(t *testing.T) {
	fc := &fakeConfig{}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/scenarios", map[string]any{
		"code": "passive", "name": "被动", "engine": "solo", "solo_hunter_id": "h-recon",
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fc.savedScenario == nil {
		t.Fatal("SaveScenario 未被调用")
	}
	if fc.savedScenario.SoloHunterID == nil || *fc.savedScenario.SoloHunterID != "h-recon" {
		t.Fatalf("SoloHunterID 未透传: %+v", fc.savedScenario)
	}
}

// TestPostHunter_ValidationRejects：缺 code/kind 或 kind 非法 → 400 中文。
func TestPostHunter_ValidationRejects(t *testing.T) {
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}})
	defer srv.Close()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"缺 code", map[string]any{"name": "n", "kind": "domain"}},
		{"缺 kind", map[string]any{"code": "c", "name": "n"}},
		{"kind=foo", map[string]any{"code": "c", "name": "n", "kind": "foo"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, "POST", srv.URL+"/hunters", tc.body)
			if code != 400 {
				t.Fatalf("want 400, got %d (%v)", code, body)
			}
			if _, ok := body["error"].(string); !ok {
				t.Fatalf("缺中文 error 字段: %v", body)
			}
		})
	}
}

// TestPostHunter_WiresCliTools：POST /hunters 带 cli_tools → SaveHunter 收到白名单透传。
func TestPostHunter_WiresCliTools(t *testing.T) {
	fc := &fakeConfig{}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/hunters", map[string]any{
		"code": "recon", "name": "侦察", "kind": "domain", "cli_tools": []string{"nmap", "nuclei"},
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fc.savedHunter == nil {
		t.Fatal("SaveHunter 未被调用")
	}
	if len(fc.savedHunter.CliTools) != 2 || fc.savedHunter.CliTools[0] != "nmap" || fc.savedHunter.CliTools[1] != "nuclei" {
		t.Fatalf("cli_tools 未透传: %+v", fc.savedHunter.CliTools)
	}
}

// TestDeleteHunter_RestrictConflict：底层 FK RESTRICT（被 solo 场景引用）→ 409。
func TestDeleteHunter_RestrictConflict(t *testing.T) {
	fc := &fakeConfig{
		hunters:      map[string]cfghunter.Hunter{"h-1": {ID: "h-1", Code: "recon", Kind: cfghunter.KindDomain}},
		deleteHunter: &pgconn.PgError{Code: "23503", Message: "FK violation"},
	}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, body := doJSON(t, "DELETE", srv.URL+"/hunters/h-1", nil)
	if code != 409 {
		t.Fatalf("want 409, got %d (%v)", code, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("缺中文 error 字段: %v", body)
	}
}

// TestListTooling_ReturnsFlatCatalog：GET /tooling/tools 分组扁平化返回 name+category+description。
func TestListTooling_ReturnsFlatCatalog(t *testing.T) {
	ft := &fakeTooling{byCat: map[string][]manifest.Tool{
		"recon": {{Name: "nmap", Category: "recon", Description: "端口扫描"}},
	}}
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}, ToolsManifest: ft})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/tooling/tools", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	arr, _ := body["tools"].([]any)
	if len(arr) != 1 {
		t.Fatalf("want 1 tool, got %d (%v)", len(arr), body)
	}
	first, _ := arr[0].(map[string]any)
	for _, k := range []string{"name", "category", "description"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("缺字段 %q: %v", k, first)
		}
	}
}
