package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// httpRequestTool 会话连续性（cookie jar）与回显保真度的通用契约测试。
// 夹具全部 httptest 级——不依赖任何真实靶机。

func newHTTPToolForTest() *httpRequestTool {
	return newHTTPRequestTool(Deps{}, 5*time.Second, false)
}

func runHTTPTool(t *testing.T, tool *httpRequestTool, args string) map[string]interface{} {
	t.Helper()
	res, err := tool.Execute(context.Background(), json.RawMessage(args))
	if err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("工具报错: %s", res.Error)
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(res.Output), &out); err != nil {
		t.Fatalf("输出应为 JSON: %v\n%s", err, res.Output)
	}
	return out
}

// 会话连续性：登录响应 Set-Cookie 的会话标识对同 host 后续请求自动生效。
func TestHTTPTool_CookieJarSessionContinuity(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("user") == "admin" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s3cret"})
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("logged in"))
			return
		}
		w.WriteHeader(http.StatusUnauthorized)
	})
	mux.HandleFunc("/protected", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("session"); err == nil && c.Value == "s3cret" {
			_, _ = w.Write([]byte("welcome admin"))
			return
		}
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("login required"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	tool := newHTTPToolForTest()

	// 1. 登录：响应落 jar
	out := runHTTPTool(t, tool, `{"url":"`+srv.URL+`/login","method":"POST","body":"user=admin"}`)
	sess := out["session"].(map[string]interface{})
	if names, _ := sess["cookies"].([]interface{}); len(names) != 1 || names[0] != "session" {
		t.Fatalf("登录后 jar 应含 session cookie, got %v", sess["cookies"])
	}

	// 2. 受保护页：未显式带 Cookie，jar 自动附加 → 200
	out = runHTTPTool(t, tool, `{"url":"`+srv.URL+`/protected"}`)
	resp := out["response"].(map[string]interface{})
	if code, _ := resp["status_code"].(float64); code != 200 {
		t.Fatalf("jar 会话应自动生效（预期 200）, got %v", resp["status_code"])
	}
	if !strings.Contains(resp["body"].(string), "welcome admin") {
		t.Fatalf("受保护内容应可达, got %v", resp["body"])
	}

	// 3. 回显保真：request.headers 应含实际附加的 Cookie（LLM 拷贝构造 repro 用）
	reqObj := out["request"].(map[string]interface{})
	hdrs, _ := reqObj["headers"].(map[string]interface{})
	if hdrs["cookie"] != "session=s3cret" { // flattenHeaders key 统一小写
		t.Fatalf("request.headers 应回显 jar 附加的 Cookie, got %v", hdrs["cookie"])
	}
}

// 显式 Cookie 头优先：调用方自带 Cookie 时不叠加 jar（避免双份/冲突）。
func TestHTTPTool_ExplicitCookieWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("cookie=" + r.Header.Get("Cookie")))
	}))
	defer srv.Close()

	tool := newHTTPToolForTest()
	// 先种 jar
	runHTTPTool(t, tool, `{"url":"`+srv.URL+`/seed"}`) // 无 Set-Cookie，jar 为空
	tool.mu.Lock()
	tool.jar["127.0.0.1|stale"] = "jar-value"
	tool.mu.Unlock()

	out := runHTTPTool(t, tool, `{"url":"`+srv.URL+`/x","headers":{"Cookie":"explicit=1"}}`)
	body := out["response"].(map[string]interface{})["body"].(string)
	if body != "cookie=explicit=1" {
		t.Fatalf("显式 Cookie 应优先（不叠加 jar）, got %q", body)
	}
	sess := out["session"].(map[string]interface{})
	if sess["attached_from_jar"] != false {
		t.Fatal("显式携带时应标记 attached_from_jar=false")
	}
}

// 会话隔离：不同 host 的 cookie 不串（host:port 粒度）。
func TestHTTPTool_CookieJarHostIsolation(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "a_session", Value: "AAA"})
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// srvB 检查：不应收到 srvA 的 cookie
		if r.Header.Get("Cookie") != "" {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("cookie leaked: " + r.Header.Get("Cookie")))
			return
		}
		_, _ = w.Write([]byte("clean"))
	}))
	defer srvB.Close()

	tool := newHTTPToolForTest()
	runHTTPTool(t, tool, `{"url":"`+srvA.URL+`/seed"}`)

	out := runHTTPTool(t, tool, `{"url":"`+srvB.URL+`/check"}`)
	resp := out["response"].(map[string]interface{})
	if code, _ := resp["status_code"].(float64); code != 200 {
		t.Fatalf("跨 host 不应串 cookie, got %v: %v", resp["status_code"], resp["body"])
	}
}

// 过期语义：Set-Cookie Max-Age<1 视为删除，jar 移除。
func TestHTTPTool_CookieExpiry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/logout" {
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "", MaxAge: -1})
			_, _ = w.Write([]byte("bye"))
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "live"})
		_, _ = w.Write([]byte("hi"))
	}))
	defer srv.Close()

	tool := newHTTPToolForTest()
	runHTTPTool(t, tool, `{"url":"`+srv.URL+`/login"}`)
	if names := tool.sessionCookieNames("x-invalid"); len(names) != 0 { // sanity: 错 host 为空
		t.Fatal("host 隔离 sanity 失败")
	}
	runHTTPTool(t, tool, `{"url":"`+srv.URL+`/logout"}`)
	host := strings.TrimPrefix(srv.URL, "http://")
	if names := tool.sessionCookieNames(host); len(names) != 0 {
		t.Fatalf("logout 后 jar 应清空 session, got %v", names)
	}
}
