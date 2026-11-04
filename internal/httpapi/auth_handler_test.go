package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// authTestEnv 聚合一个 auth 测试服务器与其 fake store / fake audit。
type authTestEnv struct {
	srv   *httptest.Server
	store *fakeAuthStore
	audit *fakeAudit
}

func newAuthTestServer(t *testing.T, store *fakeAuthStore) *authTestEnv {
	t.Helper()
	ev := &authTestEnv{store: store, audit: &fakeAudit{}}
	ev.srv = newTestServer(t, Deps{Auth: store, AuthAudit: ev.audit})
	t.Cleanup(func() { ev.srv.Close() })
	return ev
}

// authedReq 发一条带可选会话 Cookie 的 JSON 请求。
func authedReq(t *testing.T, method, url, body string, cookie *http.Cookie) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, url, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if cookie != nil {
		req.AddCookie(cookie) // #nosec G124 // 测试注入会话凭证
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

// sessionFrom 从响应提取会话 Cookie；无则返回 nil。
func sessionFrom(t *testing.T, resp *http.Response) *http.Cookie {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	for _, c := range resp.Cookies() {
		if c.Name == sessionCookieName {
			return c
		}
	}
	return nil
}

func decodeJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// TestAuthLogin 验证登录：成功签发会话；密码错/用户名不存在统一 401 且文案一致（防枚举）。
func TestAuthLogin(t *testing.T) {
	ev := newAuthTestServer(t, newFakeAuthStore())

	resp := authedReq(t, "POST", ev.srv.URL+"/api/auth/login",
		`{"username":"`+testAccountName+`","password":"`+testAccountPass+`"}`, nil)
	if resp.StatusCode != 200 {
		t.Fatalf("正确凭据应 200，得 %d", resp.StatusCode)
	}
	var body struct {
		Username string `json:"username"`
	}
	decodeJSON(t, resp, &body)
	if body.Username != testAccountName {
		t.Errorf("应回显用户名，得 %q", body.Username)
	}

	// 密码错误与用户名不存在：同一状态码同一文案。
	r1 := authedReq(t, "POST", ev.srv.URL+"/api/auth/login",
		`{"username":"`+testAccountName+`","password":"wrong-pass-x"}`, nil)
	r2 := authedReq(t, "POST", ev.srv.URL+"/api/auth/login",
		`{"username":"ghost-user","password":"whatever-x"}`, nil)
	if r1.StatusCode != 401 || r2.StatusCode != 401 {
		t.Fatalf("两类失败都应 401，得 %d/%d", r1.StatusCode, r2.StatusCode)
	}
	var e1, e2 map[string]string
	decodeJSON(t, r1, &e1)
	decodeJSON(t, r2, &e2)
	if e1["error"] != e2["error"] {
		t.Errorf("失败文案应一致防枚举: %q vs %q", e1["error"], e2["error"])
	}
	if !ev.audit.containsAction(authActionLoginFailed) {
		t.Error("登录失败应落审计")
	}
}

// TestAuthLogin_RateLimit 验证：同一 username+IP 连续 5 次失败后第 6 次 429。
func TestAuthLogin_RateLimit(t *testing.T) {
	ev := newAuthTestServer(t, newFakeAuthStore())
	for i := 0; i < comboLockFails; i++ {
		resp := authedReq(t, "POST", ev.srv.URL+"/api/auth/login",
			`{"username":"`+testAccountName+`","password":"bad-pass-xx"}`, nil)
		_ = resp.Body.Close()
	}
	resp := authedReq(t, "POST", ev.srv.URL+"/api/auth/login",
		`{"username":"`+testAccountName+`","password":"`+testAccountPass+`"}`, nil)
	if resp.StatusCode != 429 {
		t.Fatalf("锁定期内即使凭据正确也应 429，得 %d", resp.StatusCode)
	}
	if resp.Header.Get("Retry-After") == "" {
		t.Error("429 应带 Retry-After")
	}
	_ = resp.Body.Close()
}

// TestAuthLogout 验证：登出删会话行 + 过期清 Cookie；带着已失效会话再登出也 200。
func TestAuthLogout(t *testing.T) {
	ev := newAuthTestServer(t, newFakeAuthStore())

	resp := authedReq(t, "POST", ev.srv.URL+"/api/auth/logout", "", testSessionCookie())
	if resp.StatusCode != 200 {
		t.Fatalf("登出应 200，得 %d", resp.StatusCode)
	}
	ck := sessionFrom(t, resp)
	if ck == nil || ck.MaxAge >= 0 {
		t.Fatal("登出应过期清除会话 Cookie（MaxAge<0）")
	}

	// 预置会话已删：同一 token 访问业务端点应 401。
	r := authedReq(t, "GET", ev.srv.URL+"/api/credentials", "", testSessionCookie())
	if r.StatusCode != 401 {
		t.Fatalf("登出后会话应失效，得 %d", r.StatusCode)
	}
	_ = r.Body.Close()

	// 无 Cookie 登出（幂等清 Cookie）也 200。
	r2 := authedReq(t, "POST", ev.srv.URL+"/api/auth/logout", "", nil)
	if r2.StatusCode != 200 {
		t.Fatalf("无会话登出应 200，得 %d", r2.StatusCode)
	}
	_ = r2.Body.Close()
}

// TestAuthMe 验证：会话内返回当前账号；无会话 401。
func TestAuthMe(t *testing.T) {
	ev := newAuthTestServer(t, newFakeAuthStore())

	var me struct {
		Username string `json:"username"`
	}
	decodeJSON(t, authedReq(t, "GET", ev.srv.URL+"/api/auth/me", "", testSessionCookie()), &me)
	if me.Username != testAccountName {
		t.Errorf("me 应返回当前账号，得 %q", me.Username)
	}

	r := authedReq(t, "GET", ev.srv.URL+"/api/auth/me", "", nil)
	if r.StatusCode != 401 {
		t.Fatalf("无会话应 401，得 %d", r.StatusCode)
	}
	_ = r.Body.Close()
}

// TestAuthChangePassword 验证：当前密码错 401；改密成功后哈希更新、
// 全部会话吊销（含当前）、Cookie 清除、新密码可登录。
func TestAuthChangePassword(t *testing.T) {
	ev := newAuthTestServer(t, newFakeAuthStore())

	// 当前密码错误。
	r := authedReq(t, "POST", ev.srv.URL+"/api/auth/password",
		`{"current_password":"bad-current","new_password":"brand-new-pass"}`, testSessionCookie())
	if r.StatusCode != 401 {
		t.Fatalf("当前密码错应 401，得 %d", r.StatusCode)
	}
	_ = r.Body.Close()

	// 改密成功。
	resp := authedReq(t, "POST", ev.srv.URL+"/api/auth/password",
		`{"current_password":"`+testAccountPass+`","new_password":"brand-new-pass"}`, testSessionCookie())
	if resp.StatusCode != 200 {
		t.Fatalf("改密应 200，得 %d", resp.StatusCode)
	}
	if ck := sessionFrom(t, resp); ck == nil || ck.MaxAge >= 0 {
		t.Fatal("改密后应过期清除会话 Cookie")
	}

	// 旧会话已吊销（含当前）。
	r2 := authedReq(t, "GET", ev.srv.URL+"/api/auth/me", "", testSessionCookie())
	if r2.StatusCode != 401 {
		t.Fatalf("改密后旧会话应全部失效，得 %d", r2.StatusCode)
	}
	_ = r2.Body.Close()

	// 新密码可登录，旧密码不行。
	r3 := authedReq(t, "POST", ev.srv.URL+"/api/auth/login",
		`{"username":"`+testAccountName+`","password":"brand-new-pass"}`, nil)
	if r3.StatusCode != 200 {
		t.Fatalf("新密码应能登录，得 %d", r3.StatusCode)
	}
	_ = r3.Body.Close()
	if !ev.audit.containsAction(authActionPasswd) {
		t.Error("改密应落审计")
	}
}

// TestAuthPasswordValidation 验证：新密码过短 / 超长 / 与当前相同 → 400。
func TestAuthPasswordValidation(t *testing.T) {
	ev := newAuthTestServer(t, newFakeAuthStore())
	cases := []struct {
		name string
		body string
	}{
		{"过短", `{"current_password":"` + testAccountPass + `","new_password":"short"}`},
		{"超长", `{"current_password":"` + testAccountPass + `","new_password":"` + strings.Repeat("x", 73) + `"}`},
		{"与当前相同", `{"current_password":"` + testAccountPass + `","new_password":"` + testAccountPass + `"}`},
	}
	for _, tc := range cases {
		r := authedReq(t, "POST", ev.srv.URL+"/api/auth/password", tc.body, testSessionCookie())
		if r.StatusCode != 400 {
			t.Errorf("%s 应 400，得 %d", tc.name, r.StatusCode)
		}
		_ = r.Body.Close()
	}
}

// containsAction 报告是否已落过指定 action 的审计事件。
func (f *fakeAudit) containsAction(action string) bool {
	for _, e := range f.events {
		if e.Action == action {
			return true
		}
	}
	return false
}
