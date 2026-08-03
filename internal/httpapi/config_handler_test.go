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
	cfgplaybook "github.com/V3teran/liusha/internal/config/playbook"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
)

// fakeConfig 是 ConfigAPI 的内存实现，记录调用以断言 handler 编排。
type fakeConfig struct {
	scenarios []cfgscenario.Scenario
	playbooks map[string]cfgplaybook.Playbook // by id
	hunters   map[string]cfghunter.Hunter     // by id

	savedPlaybook  *cfgplaybook.NewParams
	setHuntersCall *setHuntersArgs
	deletePlaybook error // DeletePlaybook 返回的错误（模拟 RESTRICT）
}

type setHuntersArgs struct {
	playbookID string
	items      []cfgplaybook.PlaybookHunter
}

func (f *fakeConfig) ListScenarios(_ context.Context, _ bool) ([]cfgscenario.Scenario, error) {
	return f.scenarios, nil
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
	return cfgscenario.Scenario{ID: "sc-new", Code: p.Code, Name: p.Name, Engine: p.Engine, PlaybookID: p.PlaybookID}, nil
}
func (f *fakeConfig) DeleteScenario(_ context.Context, _, _ string) error { return nil }

func (f *fakeConfig) ListPlaybooks(_ context.Context) ([]cfgplaybook.Playbook, error) {
	out := make([]cfgplaybook.Playbook, 0, len(f.playbooks))
	for _, p := range f.playbooks {
		out = append(out, p)
	}
	return out, nil
}
func (f *fakeConfig) PlaybookByID(_ context.Context, id string) (cfgplaybook.Playbook, error) {
	if p, ok := f.playbooks[id]; ok {
		return p, nil
	}
	return cfgplaybook.Playbook{}, pgxErrNoRows()
}
func (f *fakeConfig) PlaybookHunters(_ context.Context, _ string) ([]cfghunter.Hunter, error) {
	return nil, nil
}
func (f *fakeConfig) SavePlaybook(_ context.Context, p cfgplaybook.NewParams) (cfgplaybook.Playbook, error) {
	f.savedPlaybook = &p
	return cfgplaybook.Playbook{ID: "pb-1", Code: p.Code, Name: p.Name}, nil
}
func (f *fakeConfig) SetHunters(_ context.Context, playbookID string, items []cfgplaybook.PlaybookHunter) error {
	f.setHuntersCall = &setHuntersArgs{playbookID: playbookID, items: items}
	return nil
}
func (f *fakeConfig) DeletePlaybook(_ context.Context, _, _ string) error { return f.deletePlaybook }

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
	return cfghunter.Hunter{ID: "h-new", Code: p.Code, Kind: p.Kind, Name: p.Name}, nil
}
func (f *fakeConfig) DeleteHunter(_ context.Context, _, _ string) error { return nil }

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

// TestListScenarios_EnabledFieldTrim：GET /scenarios 字段裁剪为 {id,code,name,description}。
func TestListScenarios_EnabledFieldTrim(t *testing.T) {
	fc := &fakeConfig{scenarios: []cfgscenario.Scenario{
		{ID: "s1", Code: "web-pentest", Name: "Web 渗透", Description: "d", Instruction: "SECRET", Engine: "swarm"},
	}}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/scenarios", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	arr, _ := body["scenarios"].([]any)
	if len(arr) != 1 {
		t.Fatalf("want 1 scenario, got %d", len(arr))
	}
	first, _ := arr[0].(map[string]any)
	if _, leaked := first["instruction"]; leaked {
		t.Fatalf("instruction 泄露到列表响应: %v", first)
	}
	if _, leaked := first["engine"]; leaked {
		t.Fatalf("engine 泄露到列表响应: %v", first)
	}
	for _, k := range []string{"id", "code", "name", "description"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("缺字段 %q: %v", k, first)
		}
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

// TestPutPlaybook_WiresHuntersInOrder：PUT /playbooks/:id 带 hunters → SavePlaybook + SetHunters 按序。
func TestPutPlaybook_WiresHuntersInOrder(t *testing.T) {
	fc := &fakeConfig{playbooks: map[string]cfgplaybook.Playbook{
		"pb-1": {ID: "pb-1", Code: "web-pentest", Name: "Web"},
	}}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/playbooks/pb-1", map[string]any{
		"code": "web-pentest", "name": "Web", "hunters": []string{"h-recon", "h-exploit"},
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fc.savedPlaybook == nil {
		t.Fatal("SavePlaybook 未被调用")
	}
	if fc.setHuntersCall == nil {
		t.Fatal("SetHunters 未被调用")
	}
	items := fc.setHuntersCall.items
	if len(items) != 2 || items[0].HunterID != "h-recon" || items[0].Position != 0 ||
		items[1].HunterID != "h-exploit" || items[1].Position != 1 {
		t.Fatalf("hunters 组合顺序错误: %+v", items)
	}
}

// TestDeletePlaybook_RestrictConflict：底层 FK RESTRICT → 409。
func TestDeletePlaybook_RestrictConflict(t *testing.T) {
	fc := &fakeConfig{
		playbooks:      map[string]cfgplaybook.Playbook{"pb-1": {ID: "pb-1", Code: "web-pentest"}},
		deletePlaybook: &pgconn.PgError{Code: "23503", Message: "FK violation"},
	}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, body := doJSON(t, "DELETE", srv.URL+"/playbooks/pb-1", nil)
	if code != 409 {
		t.Fatalf("want 409, got %d (%v)", code, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("缺中文 error 字段: %v", body)
	}
}
