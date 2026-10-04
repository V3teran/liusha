package executor

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// 旧格式（traffic_id/modifications 引用流量表）必须被拒——配方必须自包含。
func TestReplay_RejectsLegacyTrafficID(t *testing.T) {
	r := NewReplayer()
	_, err := r.Replay(context.Background(), json.RawMessage(`{"traffic_id":1,"modifications":{"body":"x"},"assert":{"status_code":200}}`))
	if err == nil || !strings.Contains(err.Error(), "旧格式") {
		t.Fatalf("旧格式配方应被拒: err=%v", err)
	}
}

// 空断言不可坐实：无谓词即橡皮图章。
func TestReplay_RejectsEmptyAssert(t *testing.T) {
	r := NewReplayer()
	_, err := r.Replay(context.Background(), json.RawMessage(`{"request":{"method":"GET","url":"http://t/api?id=1","headers":{},"body":""},"assert":{}}`))
	if err == nil || !strings.Contains(err.Error(), "assert 为空") {
		t.Fatalf("空断言应被拒: err=%v", err)
	}
}

// 非完整 URL（无 scheme）必须被拒——防 'unsupported protocol scheme' 类无效复放。
func TestReplay_RejectsRelativeURL(t *testing.T) {
	r := NewReplayer()
	_, err := r.Replay(context.Background(), json.RawMessage(`{"request":{"method":"GET","url":"/.hidden","headers":{},"body":""},"assert":{"status_code":200}}`))
	if err == nil || !strings.Contains(err.Error(), "完整 URL") {
		t.Fatalf("相对 URL 应被拒: err=%v", err)
	}
}

// body_not_contains 别名（prompt 侧口径）与 body_absent 等价。
func TestReplay_AcceptsBodyNotContainsAlias(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"user":"alice"}`))
	}))
	defer srv.Close()

	r := NewReplayer()
	recipe := `{"request":{"method":"GET","url":"` + srv.URL + `","headers":{},"body":""},"assert":{"body_not_contains":["login required"]}}`
	res, err := r.Replay(context.Background(), json.RawMessage(recipe))
	if err != nil {
		t.Fatalf("别名断言应可用: %v", err)
	}
	var ev struct {
		AssertPassed bool `json:"assert_passed"`
	}
	if err := json.Unmarshal(res.Evaluation, &ev); err != nil || !ev.AssertPassed {
		t.Fatalf("body_not_contains 应按 body_absent 语义判定: ev=%s err=%v", res.Evaluation, err)
	}
}

// 配方带 baseline 时：断言在基线也全命中 = 无鉴别力，拒绝坐实（差分铁律）。
func TestReplay_RejectsIndiscriminateAssertOnBaseline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("welcome to the app: user=" + r.URL.Query().Get("id")))
	}))
	defer srv.Close()

	r := NewReplayer()
	recipe := map[string]interface{}{
		"request":  map[string]interface{}{"method": "GET", "url": srv.URL + "?id=1%27+UNION+SELECT+1--", "headers": map[string]string{}, "body": ""},
		"baseline": map[string]interface{}{"method": "GET", "url": srv.URL + "?id=1", "headers": map[string]string{}, "body": ""},
		"assert":   map[string]interface{}{"status_code": 200, "body_contains": []string{"welcome"}},
	}
	b, _ := json.Marshal(recipe)
	_, err := r.Replay(context.Background(), b)
	if err == nil || !strings.Contains(err.Error(), "无鉴别力") {
		t.Fatalf("基线同样命中的断言应被拒: err=%v", err)
	}
}

// 自包含配方全链：基线不命中、攻击命中 → 证据含双对照与断言明细。
func TestReplay_SelfContainedWithBaseline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.RawQuery, "UNION") {
			_, _ = w.Write([]byte("dump: root:toor admin"))
			return
		}
		_, _ = w.Write([]byte("user=1"))
	}))
	defer srv.Close()

	r := NewReplayer()
	recipe := map[string]interface{}{
		"request":  map[string]interface{}{"method": "GET", "url": srv.URL + "?id=1%27+UNION+SELECT+1--", "headers": map[string]string{"user-agent": "liusha-test"}, "body": ""},
		"baseline": map[string]interface{}{"method": "GET", "url": srv.URL + "?id=1", "headers": map[string]string{}, "body": ""},
		"assert":   map[string]interface{}{"body_contains": []string{"root:toor"}},
	}
	b, _ := json.Marshal(recipe)
	res, err := r.Replay(context.Background(), b)
	if err != nil {
		t.Fatalf("自包含配方应可复现: %v", err)
	}
	var ev struct {
		HasBaseline    bool   `json:"has_baseline"`
		BaselineStatus int    `json:"baseline_status_code"`
		AttackStatus   int    `json:"attack_status_code"`
		URL            string `json:"url"`
		AssertPassed   bool   `json:"assert_passed"`
		Snippet        string `json:"response_body_snippet"`
	}
	if err := json.Unmarshal(res.Evaluation, &ev); err != nil {
		t.Fatalf("证据应可解析: %v", err)
	}
	if !ev.HasBaseline || ev.BaselineStatus != 200 || ev.AttackStatus != 200 {
		t.Fatalf("证据应含基线/攻击双对照: %+v", ev)
	}
	if !ev.AssertPassed || !strings.Contains(ev.Snippet, "root:toor") {
		t.Fatalf("攻击断言应命中: %+v", ev)
	}
	if !strings.HasPrefix(ev.URL, "http://") {
		t.Fatalf("证据 url 应为完整 URL: %s", ev.URL)
	}
}

// ─── 域信封分发 ───────────────────────────────────────────────────────────────

// 信封化 web 配方（domain=web + recipe/assert 分离）走机器重放。
func TestReplay_EnvelopeWebDispatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("leaked: admin:pw"))
	}))
	defer srv.Close()

	env := map[string]interface{}{
		"domain": "web",
		"recipe": map[string]interface{}{
			"request": map[string]interface{}{"method": "GET", "url": srv.URL, "headers": map[string]string{}, "body": ""},
		},
		"assert": map[string]interface{}{"body_contains": []string{"admin:pw"}},
	}
	b, _ := json.Marshal(env)
	res, err := NewReplayer().Replay(context.Background(), b)
	if err != nil {
		t.Fatalf("信封 web 配方应走机器重放: %v", err)
	}
	var ev struct {
		AssertPassed bool `json:"assert_passed"`
	}
	if err := json.Unmarshal(res.Evaluation, &ev); err != nil || !ev.AssertPassed {
		t.Fatalf("信封 web 断言应命中: %s err=%v", res.Evaluation, err)
	}
}

// generic 域：无机器重放——配方原样回显给裁决官，不执行任何动作。
func TestReplay_EnvelopeGenericEcho(t *testing.T) {
	env := json.RawMessage(`{"domain":"generic","recipe":{"steps":"1. 登录后台 2. 导出配置观察明文密码"},"assert":{"description":"导出文件含明文密码"}}`)
	res, err := NewReplayer().Replay(context.Background(), env)
	if err != nil {
		t.Fatalf("generic 配方应回显不报错: %v", err)
	}
	var ev struct {
		Domain string          `json:"domain"`
		Recipe json.RawMessage `json:"recipe"`
		Note   string          `json:"note"`
	}
	if err := json.Unmarshal(res.Evaluation, &ev); err != nil {
		t.Fatalf("generic 证据应可解析: %v", err)
	}
	if ev.Domain != "generic" || len(ev.Recipe) == 0 || !strings.Contains(ev.Note, "run_command") {
		t.Fatalf("generic 证据应含域标签/配方/裁决官指引: %+v", ev)
	}
	if res.DurationMs != 0 {
		t.Fatalf("generic 无机器执行，耗时应为 0, got %d", res.DurationMs)
	}
}

// 未知域拒绝；无信封且无 request 的裸配方拒绝。
func TestReplay_EnvelopeDomainGuards(t *testing.T) {
	r := NewReplayer()
	if _, err := r.Replay(context.Background(), json.RawMessage(`{"domain":"binary","recipe":{"gdb":"run"},"assert":{"x":1}}`)); err == nil ||
		!strings.Contains(err.Error(), "未知域") {
		t.Fatalf("未知域应被拒: err=%v", err)
	}
	if _, err := r.Replay(context.Background(), json.RawMessage(`{"assert":{"status_code":200}}`)); err == nil ||
		!strings.Contains(err.Error(), "domain 信封") {
		t.Fatalf("无信封无 request 的裸配方应被拒: err=%v", err)
	}
}
