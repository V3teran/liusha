package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/agent"
	"github.com/V3teran/liusha/internal/configstore"
	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/toolstore"
)

// fakeConfig 是 ConfigAPI 的可编程桩：记录最近一次写入的参数。
type fakeConfig struct {
	agents []agent.Profile

	toggled struct {
		code, field, elem string
		on                bool
	}
	patched     override.Document
	resetFields []string
	archiveGone string
	patchErr    error
}

func (f *fakeConfig) Agents(context.Context) ([]agent.Profile, error) { return f.agents, nil }

func (f *fakeConfig) AgentDetails(context.Context) ([]configstore.AgentDetail, error) {
	out := make([]configstore.AgentDetail, 0, len(f.agents))
	for _, a := range f.agents {
		out = append(out, configstore.AgentDetail{Profile: a, Overrides: map[string]override.State{}})
	}
	return out, nil
}

func (f *fakeConfig) AgentDetail(ctx context.Context, code string) (configstore.AgentDetail, error) {
	all, _ := f.AgentDetails(ctx)
	for _, d := range all {
		if d.Profile.Code == code {
			return d, nil
		}
	}
	return configstore.AgentDetail{}, fmt.Errorf("agent %q %w", code, configstore.ErrNotFound)
}

func (f *fakeConfig) PatchAgent(_ context.Context, code string, changes override.Document) (agent.Profile, error) {
	f.patched = changes
	return agent.Profile{Code: code}, f.patchErr
}

func (f *fakeConfig) ToggleAgent(_ context.Context, code, field, elem string, on bool) (agent.Profile, error) {
	f.toggled.code, f.toggled.field, f.toggled.elem, f.toggled.on = code, field, elem, on
	return agent.Profile{Code: code}, nil
}

func (f *fakeConfig) ResetAgent(_ context.Context, code string, fields ...string) (agent.Profile, error) {
	f.resetFields = fields
	return agent.Profile{Code: code}, nil
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

func twoAgents() []agent.Profile {
	return []agent.Profile{
		{Code: "executor", Name: "执行者", Config: agent.Config{CliTools: []string{"*"}, FunctionTools: []string{"http_request"}}},
		{Code: "planner", Name: "规划者", Config: agent.Config{CliTools: []string{}, FunctionTools: []string{"observe_state"}}},
	}
}

func toolRouter(cfg *fakeConfig) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	tools := fakeToolCatalog{tools: []toolstore.Definition{
		{Name: "nmap", Kind: toolstore.KindCLI},
		{Name: "http_request", Kind: toolstore.KindFunction},
	}}
	r.GET("/tools/:name", getToolHandler(tools, cfg))
	r.PUT("/tools/:name/agents/:code", assignToolHandler(tools, cfg))
	return r
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
