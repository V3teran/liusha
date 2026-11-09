package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/override"
)

func agentRouter(cfg *fakeConfig) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerAgentConfigRoutes(r, Deps{ConfigStore: cfg})
	return r
}

func TestListAgents_SearchAndPage(t *testing.T) {
	r := agentRouter(&fakeConfig{agents: twoAgents()})
	var body struct {
		Agents []map[string]any `json:"agents"`
		Total  int              `json:"total"`
	}
	w := serve(r, http.MethodGet, "/agents?q=规划", "")
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusOK || body.Total != 1 || body.Agents[0]["code"] != "planner" {
		t.Fatalf("按名称搜索: %d %s", w.Code, w.Body.String())
	}
	w = serve(r, http.MethodGet, "/agents?page=2&size=1", "")
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.Total != 2 || len(body.Agents) != 1 || body.Agents[0]["code"] != "planner" {
		t.Fatalf("分页: %s", w.Body.String())
	}
	if _, ok := body.Agents[0]["customized"]; !ok {
		t.Fatalf("列表项应带已自定义字段: %s", w.Body.String())
	}
}

// 详情附只读契约：程序解析所依赖的格式随代码走、不可自定义；monitor 无契约。
func TestGetAgent_IncludesContract(t *testing.T) {
	cfg := &fakeConfig{agents: twoAgents()}
	r := agentRouter(cfg)
	w := serve(r, http.MethodGet, "/agents/planner", "")
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "should_continue") {
		t.Fatalf("planner 详情应含输出契约: %d %s", w.Code, w.Body.String())
	}
	if w := serve(r, http.MethodGet, "/agents/nope", ""); w.Code != http.StatusNotFound {
		t.Fatalf("未知 agent 应 404，得 %d", w.Code)
	}
}

func TestPatchAgent_WiresAndValidates(t *testing.T) {
	cfg := &fakeConfig{agents: twoAgents()}
	r := agentRouter(cfg)
	w := serve(r, http.MethodPatch, "/agents/planner", `{"complexity":"simple","skills":{"on":["bac"]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("得 %d (%s)", w.Code, w.Body.String())
	}
	if string(cfg.patched["complexity"]) != `"simple"` || string(cfg.patched["skills"]) != `{"on":["bac"]}` {
		t.Fatalf("接线错误: %v", cfg.patched)
	}
	if w := serve(r, http.MethodPatch, "/agents/planner", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("空提交应 400，得 %d", w.Code)
	}
	cfg.patchErr = fmt.Errorf("%w：章程不能为空", override.ErrInvalid)
	if w := serve(r, http.MethodPatch, "/agents/planner", `{"charter":""}`); w.Code != http.StatusBadRequest {
		t.Fatalf("校验失败应 400，得 %d", w.Code)
	}
}

func TestResetAgentAndArchive(t *testing.T) {
	cfg := &fakeConfig{agents: twoAgents()}
	r := agentRouter(cfg)
	if w := serve(r, http.MethodDelete, "/agents/planner/overrides/charter", ""); w.Code != http.StatusOK {
		t.Fatalf("得 %d", w.Code)
	}
	if len(cfg.resetFields) != 1 || cfg.resetFields[0] != "charter" {
		t.Fatalf("单字段恢复接线错误: %v", cfg.resetFields)
	}
	if w := serve(r, http.MethodDelete, "/agents/planner/overrides", ""); w.Code != http.StatusOK || len(cfg.resetFields) != 0 {
		t.Fatalf("全部恢复应不带字段: %d %v", w.Code, cfg.resetFields)
	}
	if w := serve(r, http.MethodDelete, "/agents/planner/prompt-archive", ""); w.Code != http.StatusNoContent || cfg.archiveGone != "planner" {
		t.Fatalf("删除归档: %d %q", w.Code, cfg.archiveGone)
	}
}
