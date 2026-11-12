package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/toolstore"
)

// fakeConfig 是 ConfigAPI 的可编程桩：记录最近一次写入的参数。
type fakeConfig struct {
	agents []agent.Agent

	toggled struct {
		code, field, elem string
		on                bool
	}
	patched     override.Document
	resetFields []string
	archiveGone string
	patchErr    error
}

func (f *fakeConfig) Agents(context.Context) ([]agent.Agent, error) { return f.agents, nil }

func (f *fakeConfig) AgentDetails(context.Context) ([]configstore.AgentDetail, error) {
	out := make([]configstore.AgentDetail, 0, len(f.agents))
	for _, a := range f.agents {
		out = append(out, configstore.AgentDetail{Agent: a, Overrides: map[string]override.State{}})
	}
	return out, nil
}

func (f *fakeConfig) AgentDetail(ctx context.Context, code string) (configstore.AgentDetail, error) {
	all, _ := f.AgentDetails(ctx)
	for _, d := range all {
		if d.Agent.Code == code {
			return d, nil
		}
	}
	return configstore.AgentDetail{}, fmt.Errorf("agent %q %w", code, configstore.ErrNotFound)
}

func (f *fakeConfig) PatchAgent(_ context.Context, code string, changes override.Document) (agent.Agent, error) {
	f.patched = changes
	return agent.Agent{Code: code}, f.patchErr
}

func (f *fakeConfig) ToggleAgent(_ context.Context, code, field, elem string, on bool) (agent.Agent, error) {
	f.toggled.code, f.toggled.field, f.toggled.elem, f.toggled.on = code, field, elem, on
	return agent.Agent{Code: code}, nil
}

func (f *fakeConfig) ResetAgent(_ context.Context, code string, fields ...string) (agent.Agent, error) {
	f.resetFields = fields
	return agent.Agent{Code: code}, nil
}

func (f *fakeConfig) DeletePromptArchive(_ context.Context, code string) error {
	f.archiveGone = code
	return nil
}

type fakeToolCatalog struct{ tools []toolstore.Definition }

func (f fakeToolCatalog) List(context.Context, toolstore.ListParams) ([]toolstore.Definition, error) {
	return f.tools, nil
}
func (f fakeToolCatalog) Count(context.Context, toolstore.ListParams) (int, error) {
	return len(f.tools), nil
}
func (f fakeToolCatalog) Get(_ context.Context, name string) (toolstore.Definition, error) {
	for _, t := range f.tools {
		if t.Name == name {
			return t, nil
		}
	}
	return toolstore.Definition{}, fmt.Errorf("tool %s 不存在", name)
}

func twoAgents() []agent.Agent {
	return []agent.Agent{
		{Code: "executor", Name: "执行者", Config: agent.Config{CliTools: []string{"*"}, FunctionTools: []string{"http_request"}}},
		{Code: "planner", Name: "规划者", Config: agent.Config{CliTools: []string{}, FunctionTools: []string{"observe_state"}}},
	}
}

// fakeInventory 是 SandboxInventoryAPI 的桩；err 非 nil 模拟沙盒不可达。
type fakeInventory struct {
	inv sandbox.Inventory
	err error
}

func (f fakeInventory) Inventory(context.Context) (sandbox.Inventory, error) { return f.inv, f.err }

func toolRouterWith(cfg *fakeConfig, inv SandboxInventoryAPI) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tools := fakeToolCatalog{tools: []toolstore.Definition{
		{Name: "nmap", Kind: toolstore.KindCLI},
		{Name: "sqlmap", Kind: toolstore.KindCLI},
		{Name: "http_request", Kind: toolstore.KindFunction},
		{Name: "drive_browser", Kind: toolstore.KindFunction},
	}}
	r.GET("/tools", listToolsHandler(tools, inv))
	r.GET("/tools/:name", getToolHandler(tools, cfg, inv))
	r.PUT("/tools/:name/agents/:code", assignToolHandler(tools, cfg))
	return r
}

func toolRouter(cfg *fakeConfig) *gin.Engine { return toolRouterWith(cfg, nil) }

// 安装状态：CLI 工具按沙盒上报，drive_browser 按浏览器能力，其余函数工具恒已安装；
// 沙盒不可达时一律 null（未知），不误报「未安装」。
func TestListTools_InstalledState(t *testing.T) {
	inv := fakeInventory{inv: sandbox.Inventory{Tools: map[string]bool{"nmap": true, "sqlmap": false}}}
	body := serve(toolRouterWith(&fakeConfig{}, inv), http.MethodGet, "/tools", "").Body.String()
	var resp struct {
		Tools []struct {
			Name      string `json:"name"`
			Installed *bool  `json:"installed"`
		} `json:"tools"`
	}
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"nmap": true, "sqlmap": false, "http_request": true, "drive_browser": false}
	if len(resp.Tools) != len(want) {
		t.Fatalf("应返回全部工具: %s", body)
	}
	for _, tool := range resp.Tools {
		if tool.Installed == nil || *tool.Installed != want[tool.Name] {
			t.Fatalf("%s installed=%v, 期望 %v", tool.Name, tool.Installed, want[tool.Name])
		}
	}

	down := fakeInventory{err: errors.New("connection refused")}
	w := serve(toolRouterWith(&fakeConfig{agents: twoAgents()}, down), http.MethodGet, "/tools/nmap", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"installed":null`) {
		t.Fatalf("沙盒不可达时安装状态应为 null: %d %s", w.Code, w.Body.String())
	}
}

// 装配态按生效名单判断，"*" 即全部。
func TestGetTool_InvolvedFollowsEffectiveList(t *testing.T) {
	r := toolRouter(&fakeConfig{agents: twoAgents()})
	w := serve(r, http.MethodGet, "/tools/nmap", "")
	if w.Code != http.StatusOK {
		t.Fatalf("得 %d", w.Code)
	}
	body := w.Body.String()
	if want := `{"code":"executor","involved":true,"name":"执行者"}`; !strings.Contains(body, want) {
		t.Fatalf("executor 名单为 * 应 involved: %s", body)
	}
	if want := `{"code":"planner","involved":false,"name":"规划者"}`; !strings.Contains(body, want) {
		t.Fatalf("planner 未装配 nmap: %s", body)
	}
}

// 装卸记为对应名单字段的单项勾选或取消。
func TestAssignTool_TogglesFieldByKind(t *testing.T) {
	cfg := &fakeConfig{agents: twoAgents()}
	r := toolRouter(cfg)
	if w := serve(r, http.MethodPut, "/tools/nmap/agents/executor", `{"involved":false}`); w.Code != http.StatusOK {
		t.Fatalf("得 %d (%s)", w.Code, w.Body.String())
	}
	if cfg.toggled.field != agent.FieldCliTools || cfg.toggled.elem != "nmap" || cfg.toggled.on {
		t.Fatalf("CLI 工具应取消 cli_tools 名单项: %+v", cfg.toggled)
	}
	if w := serve(r, http.MethodPut, "/tools/http_request/agents/planner", `{"involved":true}`); w.Code != http.StatusOK {
		t.Fatalf("得 %d", w.Code)
	}
	if cfg.toggled.field != agent.FieldFunctionTools || !cfg.toggled.on {
		t.Fatalf("函数工具应勾选 function_tools 名单项: %+v", cfg.toggled)
	}
}

// involved 必填：缺字段若按 false 处理会把格式错误的请求静默执行成「卸载」。
func TestAssignTool_InvolvedRequired(t *testing.T) {
	cfg := &fakeConfig{agents: twoAgents()}
	r := toolRouter(cfg)
	if w := serve(r, http.MethodPut, "/tools/nmap/agents/executor", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("缺 involved 应 400，得 %d", w.Code)
	}
	if cfg.toggled.code != "" {
		t.Fatal("非法请求不应触达 store")
	}
	if w := serve(r, http.MethodPut, "/tools/nope/agents/executor", `{"involved":true}`); w.Code != http.StatusNotFound {
		t.Fatalf("工具不存在应 404，得 %d", w.Code)
	}
}
