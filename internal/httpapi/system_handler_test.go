package httpapi

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/config/settingstore"
)

// fakeSettings 是 SettingsAPI 的内存实现，记录写入以断言 handler 编排。
type fakeSettings struct {
	compaction  settingstore.CompactionSettings
	runtime     settingstore.RuntimeSettings
	proxyFilter settingstore.ProxyFilterSettings

	savedCompaction  *settingstore.CompactionSettings
	savedRuntime     *settingstore.RuntimeSettings
	savedProxyFilter *settingstore.ProxyFilterSettings
}

func (f *fakeSettings) Compaction(_ context.Context) (settingstore.CompactionSettings, error) {
	return f.compaction, nil
}
func (f *fakeSettings) SaveCompaction(_ context.Context, v settingstore.CompactionSettings) error {
	f.savedCompaction = &v
	return nil
}
func (f *fakeSettings) Runtime(_ context.Context) (settingstore.RuntimeSettings, error) {
	return f.runtime, nil
}
func (f *fakeSettings) SaveRuntime(_ context.Context, v settingstore.RuntimeSettings) error {
	f.savedRuntime = &v
	return nil
}
func (f *fakeSettings) ProxyFilter(_ context.Context) (settingstore.ProxyFilterSettings, error) {
	return f.proxyFilter, nil
}
func (f *fakeSettings) SaveProxyFilter(_ context.Context, v settingstore.ProxyFilterSettings) error {
	f.savedProxyFilter = &v
	return nil
}

// TestGetCompactionSettings_ReturnsGroup：GET /settings/compaction 回 compaction 组当前旋钮。
func TestGetCompactionSettings_ReturnsGroup(t *testing.T) {
	fs := &fakeSettings{compaction: settingstore.CompactionSettings{
		PlannerWindowMessages: 20, ExecutorWindowMessages: 15, EvaluatorWindowMessages: 10, MonitorWindowMessages: 10,
	}}
	srv := newTestServer(t, Deps{Settings: fs})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/settings/compaction", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	r, _ := body["compaction"].(map[string]any)
	if r["planner_window_messages"] != float64(20) || r["monitor_window_messages"] != float64(10) {
		t.Fatalf("compaction 组未回传: %v", body["compaction"])
	}
}

// TestPutCompactionSettings_ValidationRejects：窗口条数非正 → 400 中文。
func TestPutCompactionSettings_ValidationRejects(t *testing.T) {
	srv := newTestServer(t, Deps{Settings: &fakeSettings{}})
	defer srv.Close()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"planner 窗口<=0", map[string]any{"planner_window_messages": 0, "executor_window_messages": 15, "evaluator_window_messages": 10, "monitor_window_messages": 10}},
		{"executor 窗口<=0", map[string]any{"planner_window_messages": 20, "executor_window_messages": -1, "evaluator_window_messages": 10, "monitor_window_messages": 10}},
		{"evaluator 窗口<=0", map[string]any{"planner_window_messages": 20, "executor_window_messages": 15, "evaluator_window_messages": 0, "monitor_window_messages": 10}},
		{"monitor 窗口<=0", map[string]any{"planner_window_messages": 20, "executor_window_messages": 15, "evaluator_window_messages": 10}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, "PUT", srv.URL+"/api/settings/compaction", tc.body)
			if code != 400 {
				t.Fatalf("want 400, got %d (%v)", code, body)
			}
			if _, ok := body["error"].(string); !ok {
				t.Fatalf("缺中文 error 字段: %v", body)
			}
		})
	}
}

// TestPutCompactionSettings_Wires：合法 PUT → SaveCompaction 收到全字段透传。
func TestPutCompactionSettings_Wires(t *testing.T) {
	fs := &fakeSettings{}
	srv := newTestServer(t, Deps{Settings: fs})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/settings/compaction", map[string]any{
		"planner_window_messages": 22, "executor_window_messages": 16,
		"evaluator_window_messages": 11, "monitor_window_messages": 9,
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fs.savedCompaction == nil || fs.savedCompaction.PlannerWindowMessages != 22 ||
		fs.savedCompaction.ExecutorWindowMessages != 16 || fs.savedCompaction.EvaluatorWindowMessages != 11 ||
		fs.savedCompaction.MonitorWindowMessages != 9 {
		t.Fatalf("compaction 未透传: %+v", fs.savedCompaction)
	}
}

// TestPutRuntimeSettings_ValidationRejects：任一旋钮非正 → 400。
func TestPutRuntimeSettings_ValidationRejects(t *testing.T) {
	srv := newTestServer(t, Deps{Settings: &fakeSettings{}})
	defer srv.Close()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"step_tool<=0", map[string]any{"step_tool_timeout_seconds": 0, "step_llm_timeout_seconds": 300, "run_tail_bytes": 4096}},
		{"step_llm<=0", map[string]any{"step_tool_timeout_seconds": 60, "step_llm_timeout_seconds": 0, "run_tail_bytes": 4096}},
		{"tail_bytes<=0", map[string]any{"step_tool_timeout_seconds": 60, "step_llm_timeout_seconds": 300, "run_tail_bytes": 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, "PUT", srv.URL+"/api/settings/runtime", tc.body)
			if code != 400 {
				t.Fatalf("want 400, got %d (%v)", code, body)
			}
		})
	}
}

// TestPutRuntimeSettings_Wires：合法 PUT → SaveRuntime 收到全字段透传。
func TestPutRuntimeSettings_Wires(t *testing.T) {
	fs := &fakeSettings{}
	srv := newTestServer(t, Deps{Settings: fs})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/settings/runtime", map[string]any{
		"step_tool_timeout_seconds": 90, "step_llm_timeout_seconds": 240, "run_tail_bytes": 8192,
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fs.savedRuntime == nil || fs.savedRuntime.StepToolTimeoutSeconds != 90 ||
		fs.savedRuntime.StepLLMTimeoutSeconds != 240 || fs.savedRuntime.RunTailBytes != 8192 {
		t.Fatalf("runtime 未透传: %+v", fs.savedRuntime)
	}
}

// TestGetProxyFilterSettings_ReturnsGroup：GET /settings/proxy-filter 回 proxy_filter 组。
func TestGetProxyFilterSettings_ReturnsGroup(t *testing.T) {
	fs := &fakeSettings{proxyFilter: settingstore.ProxyFilterSettings{
		AllowHosts:          []string{"*.example.com"},
		ExcludeMethods:      []string{"CONNECT"},
		MaxRequestBodySize:  1 << 20,
		MaxResponseBodySize: 1 << 20,
	}}
	srv := newTestServer(t, Deps{Settings: fs})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/settings/proxy-filter", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	pf, _ := body["proxy_filter"].(map[string]any)
	if hosts, _ := pf["allow_hosts"].([]any); len(hosts) != 1 {
		t.Fatalf("allow_hosts 未回传: %v", body["proxy_filter"])
	}
}

// TestPutProxyFilterSettings_ValidationRejects：body 上限非正 → 400。
func TestPutProxyFilterSettings_ValidationRejects(t *testing.T) {
	srv := newTestServer(t, Deps{Settings: &fakeSettings{}})
	defer srv.Close()

	cases := []struct {
		name string
		body map[string]any
	}{
		{"req_body<=0", map[string]any{"max_request_body_size": 0, "max_response_body_size": 1024}},
		{"resp_body<=0", map[string]any{"max_request_body_size": 1024, "max_response_body_size": 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, "PUT", srv.URL+"/api/settings/proxy-filter", tc.body)
			if code != 400 {
				t.Fatalf("want 400, got %d (%v)", code, body)
			}
		})
	}
}

// TestPutProxyFilterSettings_Wires：合法 PUT → SaveProxyFilter 收到过滤规则透传。
func TestPutProxyFilterSettings_Wires(t *testing.T) {
	fs := &fakeSettings{}
	srv := newTestServer(t, Deps{Settings: fs})
	defer srv.Close()

	code, _ := doJSON(t, "PUT", srv.URL+"/api/settings/proxy-filter", map[string]any{
		"allow_hosts":            []string{"*.target.com"},
		"exclude_methods":        []string{"OPTIONS", "CONNECT"},
		"exclude_status_codes":   []int{304},
		"max_request_body_size":  2 << 20,
		"max_response_body_size": 4 << 20,
	})
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if fs.savedProxyFilter == nil ||
		len(fs.savedProxyFilter.AllowHosts) != 1 || fs.savedProxyFilter.AllowHosts[0] != "*.target.com" ||
		len(fs.savedProxyFilter.ExcludeMethods) != 2 ||
		fs.savedProxyFilter.MaxRequestBodySize != 2<<20 || fs.savedProxyFilter.MaxResponseBodySize != 4<<20 {
		t.Fatalf("proxy_filter 未透传: %+v", fs.savedProxyFilter)
	}
}

// TestSettingsRoutes_NotRegisteredWhenNil：Settings 为 nil 时 /settings 路由不挂（404）。
func TestSettingsRoutes_NotRegisteredWhenNil(t *testing.T) {
	srv := newTestServer(t, Deps{})
	defer srv.Close()

	code, _ := doJSON(t, "GET", srv.URL+"/api/settings/compaction", nil)
	if code != 404 {
		t.Fatalf("Settings=nil 应 404，got %d", code)
	}
}
