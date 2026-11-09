package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/V3teran/liusha/internal/override"
	"github.com/V3teran/liusha/internal/settingstore"
)

type fakeSettings struct {
	patchedGroup settingstore.Group
	patched      override.Document
	resetGroup   settingstore.Group
	resetFields  []string
	patchErr     error
}

func (f *fakeSettings) Detail(_ context.Context, g settingstore.Group) (settingstore.GroupDetail, error) {
	return settingstore.GroupDetail{
		Values:    override.Document{"run_tail_bytes": json.RawMessage(`8192`)},
		Defaults:  override.Document{"run_tail_bytes": json.RawMessage(`8192`)},
		Overrides: map[string]override.State{},
	}, nil
}

func (f *fakeSettings) Patch(_ context.Context, g settingstore.Group, changes override.Document) error {
	f.patchedGroup, f.patched = g, changes
	return f.patchErr
}

func (f *fakeSettings) Reset(_ context.Context, g settingstore.Group, fields ...string) error {
	f.resetGroup, f.resetFields = g, fields
	return nil
}

func settingsRouter(api SettingsAPI) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerSettingsRoutes(r, Deps{Settings: api})
	return r
}

func TestSettings_UnknownGroupIs404(t *testing.T) {
	r := settingsRouter(&fakeSettings{})
	// 旧路由段 proxy-filter 不再存在：路由段即分组取值 proxy_filter。
	for _, path := range []string{"/settings/proxy-filter", "/settings/nope"} {
		if w := serve(r, http.MethodGet, path, ""); w.Code != http.StatusNotFound {
			t.Errorf("GET %s 应 404，得 %d", path, w.Code)
		}
	}
}

func TestSettings_GetReturnsDetail(t *testing.T) {
	r := settingsRouter(&fakeSettings{})
	w := serve(r, http.MethodGet, "/settings/runtime", "")
	if w.Code != http.StatusOK {
		t.Fatalf("得 %d", w.Code)
	}
	var body map[string]json.RawMessage
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	for _, k := range []string{"values", "defaults", "overrides"} {
		if _, ok := body[k]; !ok {
			t.Errorf("响应缺 %s: %s", k, w.Body.String())
		}
	}
}

func TestSettings_PatchWiresGroupAndChanges(t *testing.T) {
	fs := &fakeSettings{}
	r := settingsRouter(fs)
	w := serve(r, http.MethodPatch, "/settings/proxy_filter", `{"exclude_hosts":{"on":["cdn.example"]}}`)
	if w.Code != http.StatusOK {
		t.Fatalf("得 %d (%s)", w.Code, w.Body.String())
	}
	if fs.patchedGroup != settingstore.GroupProxyFilter || string(fs.patched["exclude_hosts"]) != `{"on":["cdn.example"]}` {
		t.Fatalf("接线错误: group=%s changes=%v", fs.patchedGroup, fs.patched)
	}
}

func TestSettings_PatchRejectsEmptyAndInvalid(t *testing.T) {
	r := settingsRouter(&fakeSettings{})
	if w := serve(r, http.MethodPatch, "/settings/runtime", `{}`); w.Code != http.StatusBadRequest {
		t.Fatalf("空提交应 400，得 %d", w.Code)
	}
	r = settingsRouter(&fakeSettings{patchErr: fmt.Errorf("%w：run_tail_bytes 必须 > 0", override.ErrInvalid)})
	if w := serve(r, http.MethodPatch, "/settings/runtime", `{"run_tail_bytes":0}`); w.Code != http.StatusBadRequest {
		t.Fatalf("校验失败应 400，得 %d", w.Code)
	}
}

func TestSettings_ResetFieldAndGroup(t *testing.T) {
	fs := &fakeSettings{}
	r := settingsRouter(fs)
	if w := serve(r, http.MethodDelete, "/settings/runtime/overrides/run_tail_bytes", ""); w.Code != http.StatusOK {
		t.Fatalf("得 %d", w.Code)
	}
	if fs.resetGroup != settingstore.GroupRuntime || len(fs.resetFields) != 1 || fs.resetFields[0] != "run_tail_bytes" {
		t.Fatalf("单字段恢复接线错误: %s %v", fs.resetGroup, fs.resetFields)
	}
	if w := serve(r, http.MethodDelete, "/settings/compaction/overrides", ""); w.Code != http.StatusOK || len(fs.resetFields) != 0 {
		t.Fatalf("整组恢复应不带字段: %d %v", w.Code, fs.resetFields)
	}
}

func TestSettingsRoutes_NotRegisteredWhenNil(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	registerSettingsRoutes(r, Deps{})
	if w := serve(r, http.MethodGet, "/settings/runtime", ""); w.Code != http.StatusNotFound {
		t.Fatalf("未注入时路由不应注册，得 %d", w.Code)
	}
}
