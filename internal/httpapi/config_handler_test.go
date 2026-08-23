package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	cfgagent "github.com/V3teran/liusha/internal/config/agent"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	cfgtool "github.com/V3teran/liusha/internal/config/tool"
)

// fakeConfig 是 ConfigAPI 的内存实现，记录调用以断言 handler 编排（playbook 层已废）。
type fakeConfig struct {
	scenarios []cfgscenario.Scenario
	executors  map[string]cfgagent.Agent // by id

	savedScenario *cfgscenario.NewParams
	savedAgent   *cfgagent.NewParams
	patchedTierID string // UpdateExecutorTier 收到的 id（断言 PATCH 编排）
	patchedTier   string // UpdateExecutorTier 收到的 tier
	deleteAgent  error  // DeleteExecutor 返回的错误（模拟 FK RESTRICT）
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
	return cfgscenario.Scenario{ID: "sc-new", Code: p.Code, Name: p.Name, Engine: p.Engine, SoloExecutorID: p.SoloExecutorID}, nil
}
func (f *fakeConfig) DeleteScenario(_ context.Context, _, _ string) error { return nil }
func (f *fakeConfig) ListScenariosPaged(_ context.Context, _ cfgscenario.ListParams) ([]cfgscenario.Scenario, error) {
	return f.scenarios, nil
}
func (f *fakeConfig) CountScenarios(_ context.Context, _ cfgscenario.ListParams) (int, error) {
	return len(f.scenarios), nil
}

func (f *fakeConfig) ListExecutors(_ context.Context, _ bool) ([]cfgagent.Agent, error) {
	out := make([]cfgagent.Agent, 0, len(f.executors))
	for _, h := range f.executors {
		out = append(out, h)
	}
	return out, nil
}
func (f *fakeConfig) ExecutorByID(_ context.Context, id string) (cfgagent.Agent, error) {
	if h, ok := f.executors[id]; ok {
		return h, nil
	}
	return cfgagent.Agent{}, pgxErrNoRows()
}
func (f *fakeConfig) ExecutorByCode(_ context.Context, code string) (cfgagent.Agent, error) {
	for _, h := range f.executors {
		if h.Code == code {
			return h, nil
		}
	}
	return cfgagent.Agent{}, pgxErrNoRows()
}
func (f *fakeConfig) SaveExecutor(_ context.Context, p cfgagent.NewParams) (cfgagent.Agent, error) {
	f.savedAgent = &p
	return cfgagent.Agent{ID: "h-new", Code: p.Code, Kind: p.Kind, Name: p.Name, FunctionTools: p.FunctionTools, CliTools: p.CliTools}, nil
}
func (f *fakeConfig) UpdateExecutorTier(_ context.Context, id, tier string) (cfgagent.Agent, error) {
	f.patchedTierID = id
	f.patchedTier = tier
	h := f.executors[id]
	h.ID = id
	h.Tier = tier
	return h, nil
}
func (f *fakeConfig) DeleteExecutor(_ context.Context, _, _ string) error { return f.deleteAgent }
func (f *fakeConfig) ListExecutorsPaged(_ context.Context, _ cfgagent.ListParams) ([]cfgagent.Agent, error) {
	out := make([]cfgagent.Agent, 0, len(f.executors))
	for _, h := range f.executors {
		out = append(out, h)
	}
	return out, nil
}
func (f *fakeConfig) CountExecutors(_ context.Context, _ cfgagent.ListParams) (int, error) {
	return len(f.executors), nil
}

// fakeToolCatalog 是 ToolCatalogAPI 的内存实现，供 /tools、/tools/:name 测试。
type fakeToolCatalog struct {
	tools      []cfgtool.Tool
	total      int
	getErr     error
	getKind    cfgtool.Kind       // Get 返回工具的 kind（空则默认 function）
	lastParams cfgtool.ListParams // 记录最近一次 List 入参，供断言分页/全量契约
}

func (f *fakeToolCatalog) List(_ context.Context, p cfgtool.ListParams) ([]cfgtool.Tool, error) {
	f.lastParams = p
	return f.tools, nil
}
func (f *fakeToolCatalog) Count(_ context.Context, _ cfgtool.ListParams) (int, error) {
	return f.total, nil
}
func (f *fakeToolCatalog) Get(_ context.Context, name string) (cfgtool.Tool, error) {
	if f.getErr != nil {
		return cfgtool.Tool{}, f.getErr
	}
	kind := f.getKind
	if kind == "" {
		kind = cfgtool.KindFunction
	}
	return cfgtool.Tool{Name: name, Kind: kind, Category: "findings", Description: "写漏洞"}, nil
}

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
// disabled）+ 全字段（含 instruction/engine/solo_agent_id/enabled）。picker 与配置页
// 共用此响应，可见 ≠ 可用交由前端按 enabled 区分（无 ?all 分流）。
func TestListScenarios_ReturnsAllWithFullFields(t *testing.T) {
	solo := "h-recon"
	fc := &fakeConfig{scenarios: []cfgscenario.Scenario{
		{ID: "s1", Code: "on", Name: "启用", Instruction: "I1", Engine: "swarm", Enabled: true},
		{ID: "s2", Code: "off", Name: "停用", Instruction: "I2", Engine: "solo", SoloExecutorID: &solo, Enabled: false},
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
	for _, k := range []string{"id", "code", "name", "description", "instruction", "engine", "solo_agent_id", "enabled"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("缺全字段 %q: %v", k, first)
		}
	}
}

// TestPostScenario_SoloRequiresAgent：solo 引擎缺 solo_agent_id → 400 中文。
func TestPostScenario_SoloRequiresAgent(t *testing.T) {
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

// TestPostScenario_SwarmRejectsAgent：swarm 引擎带 solo_agent_id → 400 中文
// （子代理池=全部 enabled 领域操作员，不接受单点指定）。
func TestPostScenario_SwarmRejectsAgent(t *testing.T) {
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}})
	defer srv.Close()

	code, body := doJSON(t, "POST", srv.URL+"/scenarios", map[string]any{
		"code": "c", "name": "n", "engine": "swarm", "solo_agent_id": "h-recon",
	})
	if code != 400 {
		t.Fatalf("want 400, got %d (%v)", code, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("缺中文 error 字段: %v", body)
	}
}

// TestPostScenario_SoloWiresAgent：solo + solo_agent_id → SaveScenario 收到 *string。
func TestPostScenario_SoloWiresAgent(t *testing.T) {
	fc := &fakeConfig{}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/scenarios", map[string]any{
		"code": "passive", "name": "被动", "engine": "solo", "solo_agent_id": "h-recon",
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fc.savedScenario == nil {
		t.Fatal("SaveScenario 未被调用")
	}
	if fc.savedScenario.SoloExecutorID == nil || *fc.savedScenario.SoloExecutorID != "h-recon" {
		t.Fatalf("SoloExecutorID 未透传: %+v", fc.savedScenario)
	}
}

// TestPostAgent_ValidationRejects：缺 code/kind 或 kind 非法 → 400 中文。
func TestPostAgent_ValidationRejects(t *testing.T) {
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
			code, body := doJSON(t, "POST", srv.URL+"/executors", tc.body)
			if code != 400 {
				t.Fatalf("want 400, got %d (%v)", code, body)
			}
			if _, ok := body["error"].(string); !ok {
				t.Fatalf("缺中文 error 字段: %v", body)
			}
		})
	}
}

// TestPostAgent_WiresCliTools：POST /executors 带 cli_tools → SaveExecutor 收到白名单透传。
func TestPostAgent_WiresCliTools(t *testing.T) {
	fc := &fakeConfig{}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, _ := doJSON(t, "POST", srv.URL+"/executors", map[string]any{
		"code": "recon", "name": "侦察", "kind": "domain", "cli_tools": []string{"nmap", "nuclei"},
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fc.savedAgent == nil {
		t.Fatal("SaveExecutor 未被调用")
	}
	if len(fc.savedAgent.CliTools) != 2 || fc.savedAgent.CliTools[0] != "nmap" || fc.savedAgent.CliTools[1] != "nuclei" {
		t.Fatalf("cli_tools 未透传: %+v", fc.savedAgent.CliTools)
	}
}

// TestDeleteAgent_RestrictConflict：底层 FK RESTRICT（被 solo 场景引用）→ 409。
func TestDeleteAgent_RestrictConflict(t *testing.T) {
	fc := &fakeConfig{
		executors: map[string]cfgagent.Agent{"h-1": {ID: "h-1", Code: "recon", Kind: cfgagent.KindExecutor}},
		deleteAgent: &pgconn.PgError{Code: "23503", Message: "FK violation"},
	}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, body := doJSON(t, "DELETE", srv.URL+"/executors/h-1", nil)
	if code != 409 {
		t.Fatalf("want 409, got %d (%v)", code, body)
	}
	if _, ok := body["error"].(string); !ok {
		t.Fatalf("缺中文 error 字段: %v", body)
	}
}

// TestListTools_ReturnsToolsAndTotal：GET /tools 返回 tools 数组 + total 计数。
func TestListTools_ReturnsToolsAndTotal(t *testing.T) {
	ft := &fakeToolCatalog{
		tools: []cfgtool.Tool{{Name: "write_finding", Kind: cfgtool.KindFunction, Category: "findings", Description: "写漏洞"}},
		total: 17,
	}
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}, ToolCatalog: ft})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/tools?kind=function&q=find&page=1&size=12", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if total, _ := body["total"].(float64); int(total) != 17 {
		t.Fatalf("want total=17, got %v", body["total"])
	}
	arr, _ := body["tools"].([]any)
	if len(arr) != 1 {
		t.Fatalf("want 1 tool, got %d (%v)", len(arr), body)
	}
	first, _ := arr[0].(map[string]any)
	for _, k := range []string{"name", "kind", "category", "description"} {
		if _, ok := first[k]; !ok {
			t.Fatalf("缺字段 %q: %v", k, first)
		}
	}
}

// TestListTools_NoPageReturnsFullList：不传 page 时不分页（Limit=0），返回过滤后全量。
// 智能体「选工具」器依赖此契约——若被默认 size 截断，超过 size 的工具将不可选。
func TestListTools_NoPageReturnsFullList(t *testing.T) {
	ft := &fakeToolCatalog{
		tools: []cfgtool.Tool{{Name: "a", Kind: cfgtool.KindCLI, Category: "recon"}},
		total: 31,
	}
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}, ToolCatalog: ft})
	defer srv.Close()

	code, _ := doJSON(t, "GET", srv.URL+"/tools?kind=cli", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if ft.lastParams.Limit != 0 {
		t.Fatalf("无 page 应不分页(Limit=0)，实际 Limit=%d", ft.lastParams.Limit)
	}
	if ft.lastParams.Offset != 0 {
		t.Fatalf("无 page 应 Offset=0，实际 %d", ft.lastParams.Offset)
	}
}

// TestListTools_PageAppliesLimit：传 page 时按 size 分页，Limit/Offset 正确换算。
func TestListTools_PageAppliesLimit(t *testing.T) {
	ft := &fakeToolCatalog{total: 31}
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}, ToolCatalog: ft})
	defer srv.Close()

	code, _ := doJSON(t, "GET", srv.URL+"/tools?page=3&size=10", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if ft.lastParams.Limit != 10 {
		t.Fatalf("want Limit=10, got %d", ft.lastParams.Limit)
	}
	if ft.lastParams.Offset != 20 {
		t.Fatalf("want Offset=20 (page3*size10), got %d", ft.lastParams.Offset)
	}
}

// TestListTools_RejectsBadKind：非法 kind 返回 400。
func TestListTools_RejectsBadKind(t *testing.T) {
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}, ToolCatalog: &fakeToolCatalog{}})
	defer srv.Close()

	code, _ := doJSON(t, "GET", srv.URL+"/tools?kind=bogus", nil)
	if code != 400 {
		t.Fatalf("want 400 for bad kind, got %d", code)
	}
}

// TestGetTool_ReturnsAgentsWithInvolved：GET /tools/:name 返回 tool + 全量智能体，
// 装配了该 function 工具的 involved=true，其余 false。
func TestGetTool_ReturnsAgentsWithInvolved(t *testing.T) {
	fc := &fakeConfig{executors: map[string]cfgagent.Agent{
		"recon": {ID: "recon", Code: "recon", Name: "侦察操作员", FunctionTools: []string{"write_finding"}},
		"web":   {ID: "web", Code: "web", Name: "Web 操作员", FunctionTools: []string{}},
	}}
	srv := newTestServer(t, Deps{ConfigStore: fc, ToolCatalog: &fakeToolCatalog{}})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/tools/write_finding", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if _, ok := body["tool"].(map[string]any); !ok {
		t.Fatalf("缺 tool 字段: %v", body)
	}
	agents, _ := body["agents"].([]any)
	if len(agents) != 2 {
		t.Fatalf("want 2 agents, got %d (%v)", len(agents), body)
	}
	involved := map[string]bool{}
	for _, a := range agents {
		m := a.(map[string]any)
		involved[m["code"].(string)] = m["involved"].(bool)
	}
	if !involved["recon"] || involved["web"] {
		t.Fatalf("involved 计算错误: %v", involved)
	}
}

// TestUpdateExecutorTier_OK：PATCH /executors/:id/tier 合法档 → 调 UpdateExecutorTier，回响带新 tier。
func TestUpdateExecutorTier_OK(t *testing.T) {
	fc := &fakeConfig{executors: map[string]cfgagent.Agent{
		"h1": {ID: "h1", Code: "planner", Kind: cfgagent.KindPlanner, Name: "编排", Tier: "heavy"},
	}}
	srv := newTestServer(t, Deps{ConfigStore: fc})
	defer srv.Close()

	code, body := doJSON(t, "PATCH", srv.URL+"/executors/h1/tier", map[string]any{"tier": "light"})
	if code != 200 {
		t.Fatalf("status=%d body=%v", code, body)
	}
	if fc.patchedTierID != "h1" || fc.patchedTier != "light" {
		t.Fatalf("want UpdateExecutorTier(h1,light), got (%q,%q)", fc.patchedTierID, fc.patchedTier)
	}
	h := body["executor"].(map[string]any)
	if h["tier"] != "light" {
		t.Fatalf("响应 tier=%v，want light", h["tier"])
	}
}

// TestUpdateExecutorTier_Rejects：非法档位（含空）→ 400，且不调 store。
func TestUpdateExecutorTier_Rejects(t *testing.T) {
	for _, bad := range []string{"", "turbo", "HEAVY"} {
		fc := &fakeConfig{}
		srv := newTestServer(t, Deps{ConfigStore: fc})
		code, _ := doJSON(t, "PATCH", srv.URL+"/executors/h1/tier", map[string]any{"tier": bad})
		srv.Close()
		if code != 400 {
			t.Fatalf("tier=%q want 400, got %d", bad, code)
		}
		if fc.patchedTierID != "" {
			t.Fatalf("tier=%q 不该调 store，却收到 id=%q", bad, fc.patchedTierID)
		}
	}
}

// TestAssignTool_AddsFunctionTool：PUT .../agents/:code involved=true → 追加到 function_tools 回存。
func TestAssignTool_AddsFunctionTool(t *testing.T) {
	fc := &fakeConfig{executors: map[string]cfgagent.Agent{
		"web": {ID: "web", Code: "web", Kind: cfgagent.KindExecutor, Name: "Web 操作员", FunctionTools: []string{}},
	}}
	srv := newTestServer(t, Deps{ConfigStore: fc, ToolCatalog: &fakeToolCatalog{}})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/tools/write_finding/agents/web", map[string]any{"involved": true})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fc.savedAgent == nil {
		t.Fatal("未回存操作员")
	}
	if got := fc.savedAgent.FunctionTools; len(got) != 1 || got[0] != "write_finding" {
		t.Fatalf("want function_tools=[write_finding], got %v", got)
	}
}

// TestAssignTool_RemovesCliTool：PUT .../agents/:code involved=false → 从 cli_tools 剔除回存（kind=cli）。
func TestAssignTool_RemovesCliTool(t *testing.T) {
	fc := &fakeConfig{executors: map[string]cfgagent.Agent{
		"web": {ID: "web", Code: "web", Kind: cfgagent.KindExecutor, Name: "Web 操作员", CliTools: []string{"nmap", "ffuf"}},
	}}
	srv := newTestServer(t, Deps{ConfigStore: fc, ToolCatalog: &fakeToolCatalog{getKind: cfgtool.KindCLI}})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/tools/nmap/agents/web", map[string]any{"involved": false})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if got := fc.savedAgent.CliTools; len(got) != 1 || got[0] != "ffuf" {
		t.Fatalf("want cli_tools=[ffuf], got %v", got)
	}
}

// TestAssignTool_ToolNotFound / AgentNotFound：工具或智能体不存在均 404，且不回存。
func TestAssignTool_ToolNotFound(t *testing.T) {
	fc := &fakeConfig{executors: map[string]cfgagent.Agent{}}
	srv := newTestServer(t, Deps{ConfigStore: fc, ToolCatalog: &fakeToolCatalog{getErr: pgxErrNoRows()}})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/tools/nope/agents/web", map[string]any{"involved": true})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
	if fc.savedAgent != nil {
		t.Fatal("工具不存在不应回存")
	}
}

func TestAssignTool_AgentNotFound(t *testing.T) {
	fc := &fakeConfig{executors: map[string]cfgagent.Agent{}}
	srv := newTestServer(t, Deps{ConfigStore: fc, ToolCatalog: &fakeToolCatalog{}})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/tools/write_finding/agents/ghost", map[string]any{"involved": true})
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
	if fc.savedAgent != nil {
		t.Fatal("智能体不存在不应回存")
	}
}

// TestGetTool_NotFound：Get 报错时返回 404。
func TestGetTool_NotFound(t *testing.T) {
	ft := &fakeToolCatalog{getErr: pgxErrNoRows()}
	srv := newTestServer(t, Deps{ConfigStore: &fakeConfig{}, ToolCatalog: ft})
	defer srv.Close()

	code, _ := doJSON(t, "GET", srv.URL+"/tools/nope", nil)
	if code != 404 {
		t.Fatalf("want 404, got %d", code)
	}
}
