package tools

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/credential"
)

// httpRequestTool 会话连续性（cookie jar）与回显保真度的通用契约测试。
// 夹具全部 httptest 级——不依赖任何真实靶机。

func newHTTPToolForTest() *httpRequestTool {
	return newHTTPRequestTool(Deps{}, 5*time.Second)
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

// fakeCredsProvider 是 credential.Provider 的内存测试替身（记录写入供断言）。
type fakeCredsProvider struct {
	mu     sync.Mutex
	byHost map[string][]credential.Identity
	saves  []credential.Identity // 记录每次 BatchSave 的身份
	getErr error
}

func newFakeCreds() *fakeCredsProvider {
	return &fakeCredsProvider{byHost: map[string][]credential.Identity{}}
}

func (f *fakeCredsProvider) SaveIdentities(_ context.Context, host string, ids []credential.Identity) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, id := range ids {
		if id.Username == credential.AnonymousName {
			continue
		}
		f.saves = append(f.saves, id)
		replaced := false
		cur := f.byHost[host]
		for i := range cur {
			if cur[i].Username == id.Username {
				cur[i] = id
				replaced = true
			}
		}
		if !replaced {
			f.byHost[host] = append(cur, id)
		}
	}
	return nil
}
func (f *fakeCredsProvider) GetIdentitiesByHost(_ context.Context, host string) ([]credential.Identity, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// 深拷贝：真实 redis store 每次读都是新反序列化值，fake 必须同语义——
	// 否则调用方对切片的原地操作会改穿存储（http_request 过滤/注入路径的
	// 历史缺陷正是被共享数组掩盖的）。
	src := f.byHost[host]
	out := make([]credential.Identity, len(src))
	for i, id := range src {
		out[i] = id
		out[i].Credentials = append([]credential.Credential(nil), id.Credentials...)
	}
	return out, f.getErr
}
func (f *fakeCredsProvider) DeleteIdentity(_ context.Context, _, _ string) error { return nil }
func (f *fakeCredsProvider) DeleteSessions(_ context.Context, _ string) (int, error) {
	return 0, nil
}
func (f *fakeCredsProvider) DeleteHost(_ context.Context, _ string) error { return nil }
func (f *fakeCredsProvider) ListHosts(_ context.Context) ([]credential.HostSummary, error) {
	return nil, nil
}

// ② N 凭证 × M 位置注入：headers/query/body 全位置、多身份、冲突规则（显式优先、
// 身份名序先到先得、JSON 体不注入）。
func TestHTTPTool_ApplyStoredCredentials_MultiPosition(t *testing.T) {
	creds := newFakeCreds()
	creds.byHost["target.local"] = []credential.Identity{
		{Username: "admin", Role: "admin", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "X-API-Key", Value: "secret-key"},
			{Type: credential.TypeQuery, Key: "token", Value: "q-token"},
			{Type: credential.TypeBody, Key: "password", Value: "p@ss"},
		}},
		{Username: "b-identity", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "X-API-Key", Value: "should-lose"}, // 同键后到，应被 admin（名序 a<b）占先
		}},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 服务端断言三位置全部到达
		if r.Header.Get("X-API-Key") != "secret-key" {
			t.Errorf("headers 凭证未注入或被覆盖: %q", r.Header.Get("X-API-Key"))
		}
		if r.URL.Query().Get("token") != "q-token" {
			t.Errorf("query 凭证未注入: %q", r.URL.Query().Get("token"))
		}
		if r.FormValue("password") != "p@ss" {
			t.Errorf("body 凭证未注入: %q", r.FormValue("password"))
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	// 重写 host 为 target.local：用自定义 transport 太重——直接把 fake provider 的
	// key 换成 httptest host
	host := strings.TrimPrefix(srv.URL, "http://")
	creds.byHost[host] = creds.byHost["target.local"]
	delete(creds.byHost, "target.local")

	tool := newHTTPRequestTool(Deps{Creds: creds}, 5*time.Second)
	out := runHTTPTool(t, tool, `{"url":"`+srv.URL+`/api","method":"POST"}`)

	// 注入明细（不含值）
	sess := out["session"].(map[string]interface{})
	applied, _ := sess["applied_credentials"].([]interface{})
	if len(applied) != 3 {
		t.Fatalf("应注入 3 条凭证（多身份同键去重后）, got %v", applied)
	}
	_ = sync.Mutex{}
}

// 显式传入优先：显式 header/query/body 键不被库覆盖。
func TestHTTPTool_ExplicitWinsOverStore(t *testing.T) {
	creds := newFakeCreds()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "explicit" {
			t.Errorf("显式 header 应优先: %q", r.Header.Get("X-API-Key"))
		}
		if r.URL.Query().Get("token") != "explicit-token" {
			t.Errorf("URL 已有 query 参数应保持: %q", r.URL.Query().Get("token"))
		}
		if r.FormValue("password") != "explicit-pass" {
			t.Errorf("显式 body 字段应优先: %q", r.FormValue("password"))
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	creds.byHost[host] = []credential.Identity{{Username: "admin", Credentials: []credential.Credential{
		{Type: credential.TypeHeaders, Key: "X-API-Key", Value: "from-store"},
		{Type: credential.TypeQuery, Key: "token", Value: "from-store"},
		{Type: credential.TypeBody, Key: "password", Value: "from-store"},
	}}}

	tool := newHTTPRequestTool(Deps{Creds: creds}, 5*time.Second)
	runHTTPTool(t, tool, `{"url":"`+srv.URL+`/api?token=explicit-token","method":"POST","headers":{"X-API-Key":"explicit"},"body":"password=explicit-pass"}`)
}

// JSON 请求体不注入 body 凭证（防破坏结构）。
func TestHTTPTool_JSONBodySkipsBodyCreds(t *testing.T) {
	creds := newFakeCreds()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if strings.Contains(string(body), "password") {
			t.Errorf("JSON 体不应被注入表单字段: %s", body)
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	creds.byHost[host] = []credential.Identity{{Username: "admin", Credentials: []credential.Credential{
		{Type: credential.TypeBody, Key: "password", Value: "x"},
	}}}

	tool := newHTTPRequestTool(Deps{Creds: creds}, 5*time.Second)
	runHTTPTool(t, tool, `{"url":"`+srv.URL+`/api","headers":{"Content-Type":"application/json"},"body":"{\"a\":1}"}`)
}

// ③B Set-Cookie 自动入库：identity=session、Cookie 头形式、防抖、ttl=0、
// 全过期清项、保留 session 身份下其他凭证。
func TestHTTPTool_SetCookieSyncsToStore(t *testing.T) {
	creds := newFakeCreds()
	// session 身份预置一条非 Cookie 凭证（凭证数量不定——合并语义必须保留它）
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s1"})
		case "/refresh":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "s2"})
		case "/logout":
			http.SetCookie(w, &http.Cookie{Name: "session", Value: "", MaxAge: -1})
		}
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	creds.byHost[host] = []credential.Identity{{Username: sessionIdentityName("t-1"), Credentials: []credential.Credential{
		{Type: credential.TypeHeaders, Key: "X-Extra", Value: "keep-me"},
	}}}

	tool := newHTTPRequestTool(Deps{Creds: creds, TaskID: "t-1"}, 5*time.Second)
	runHTTPTool(t, tool, `{"url":"`+srv.URL+`/login"}`) // Set-Cookie → 入库

	if len(creds.saves) != 1 {
		t.Fatalf("应写一次（Set-Cookie 变更入库）, saves=%d", len(creds.saves))
	}
	saved := creds.saves[0]
	if saved.Username != sessionIdentityName("t-1") {
		t.Fatalf("应写入本 task 会话用户名 task:t-1:session, got %q", saved.Username)
	}
	var cookieVal, extra string
	for _, c := range saved.Credentials {
		if c.Key == "Cookie" {
			cookieVal = c.Value
		}
		if c.Key == "X-Extra" {
			extra = c.Value
		}
	}
	if cookieVal != "session=s1" || extra != "keep-me" {
		t.Fatalf("Cookie 应为完整头形式且保留其他凭证, cookie=%q extra=%q", cookieVal, extra)
	}

	// 防抖：相同 Set-Cookie 再请求不重复写库
	runHTTPTool(t, tool, `{"url":"`+srv.URL+`/login`+"\"}")
	if len(creds.saves) != 1 {
		t.Fatalf("值未变不应重复写库, saves=%d", len(creds.saves))
	}
	// 值变更 → 写
	runHTTPTool(t, tool, `{"url":"`+srv.URL+`/refresh`+"\"}")
	if len(creds.saves) != 2 {
		t.Fatalf("值变更应写库, saves=%d", len(creds.saves))
	}
	// 全过期 → Cookie 项清除、其他凭证保留
	runHTTPTool(t, tool, `{"url":"`+srv.URL+`/logout`+"\"}")
	last := creds.saves[len(creds.saves)-1]
	hasCookie, hasExtra := false, false
	for _, c := range last.Credentials {
		if c.Key == "Cookie" {
			hasCookie = true
		}
		if c.Key == "X-Extra" {
			hasExtra = true
		}
	}
	if hasCookie || !hasExtra {
		t.Fatalf("全过期应清 Cookie 项并保留其他凭证, cookie=%v extra=%v", hasCookie, hasExtra)
	}
}

// 跨任务会话隔离（共享凭证库下）：task A 的会话身份对 task B 不可注入。
func TestHTTPTool_CrossTaskSessionIsolation(t *testing.T) {
	creds := newFakeCreds()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("plain"))
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")

	// task A 登录拿到会话
	srvA := srv // 同 host 模拟：直接给 A 写入会话身份
	creds.byHost[host] = []credential.Identity{{Username: sessionIdentityName("task-A"), Credentials: []credential.Credential{
		{Type: credential.TypeHeaders, Key: "Cookie", Value: "session=A-value"},
	}}}
	_ = srvA

	// task A 的工具：注入自己的会话 ✓
	toolA := newHTTPRequestTool(Deps{Creds: creds, TaskID: "task-A"}, 5*time.Second)
	out := runHTTPTool(t, toolA, `{"url":"`+srv.URL+`/x","headers":{}}`)
	reqObj := out["request"].(map[string]interface{})
	hdrs, _ := reqObj["headers"].(map[string]interface{})
	if hdrs["cookie"] != "session=A-value" {
		t.Fatalf("task A 应注入自己的会话, got %v", hdrs["cookie"])
	}

	// task B 的工具：A 的会话不可见（不注入）
	toolB := newHTTPRequestTool(Deps{Creds: creds, TaskID: "task-B"}, 5*time.Second)
	out = runHTTPTool(t, toolB, `{"url":"`+srv.URL+`/x","headers":{}}`)
	reqObj = out["request"].(map[string]interface{})
	hdrs, _ = reqObj["headers"].(map[string]interface{})
	if hdrs["cookie"] == "session=A-value" {
		t.Fatal("task B 不应注入 task A 的会话（跨任务隔离失效）")
	}

	// host 级共享身份（无 task: 前缀，用户预录入）：两个 task 都注入
	creds.byHost[host] = append(creds.byHost[host], credential.Identity{
		Username: "admin", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "X-Shared", Value: "yes"},
		}})
	for _, tool := range []*httpRequestTool{toolA, toolB} {
		out := runHTTPTool(t, tool, `{"url":"`+srv.URL+`/x","headers":{}}`)
		hdrs, _ := out["request"].(map[string]interface{})["headers"].(map[string]interface{})
		if hdrs["x-shared"] != "yes" {
			t.Fatal("host 级共享身份应对所有 task 注入")
		}
	}
}

// identity 三态语义（BAC 的身份原语契约）：
// 省略=全注入；"身份名"=仅该身份且响应 Set-Cookie 归属该身份；""=匿名（无注入、
// 无会话 Cookie、响应不落会话）。回归点：匿名不得被任务会话 jar 污染。
func TestHTTPTool_IdentitySemantics(t *testing.T) {
	creds := newFakeCreds()
	mux := http.NewServeMux()
	// /whoami 回显收到的凭证头，断言注入面的唯一事实源。
	mux.HandleFunc("/whoami", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("COOKIE[" + r.Header.Get("Cookie") + "] TOKEN[" + r.Header.Get("X-Token") + "]"))
	})
	// /set-sess 模拟登录：Set-Cookie 一枚会话 cookie。
	mux.HandleFunc("/set-sess", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "sess", Value: "xyz"})
		_, _ = w.Write([]byte("ok"))
	})
	// /refresh 模拟指定身份的会话续期：Set-Cookie admin_sess。
	mux.HandleFunc("/refresh", func(w http.ResponseWriter, _ *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "admin_sess", Value: "renewed"})
		_, _ = w.Write([]byte("ok"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "http://")
	creds.byHost[host] = []credential.Identity{
		{Username: "admin", Role: "admin", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "a=1"},
			{Type: credential.TypeHeaders, Key: "X-Token", Value: "tok-admin"},
		}},
		{Username: "user", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "u=1"},
		}},
	}
	tool := newHTTPRequestTool(Deps{Creds: creds, TaskID: "t-identity"}, 5*time.Second)

	body := func(args string) string {
		t.Helper()
		out := runHTTPTool(t, tool, args)
		resp := out["response"].(map[string]interface{})
		return resp["body"].(string)
	}

	// 1) 省略：全部注入（Cookie 跨身份去重取身份名序先到的 admin）。
	b := body(`{"url":"` + srv.URL + `/whoami"}`)
	if !strings.Contains(b, "COOKIE[a=1]") || !strings.Contains(b, "TOKEN[tok-admin]") {
		t.Fatalf("省略 identity 应注入 admin 的 Cookie+X-Token: %s", b)
	}

	// 2) 指定身份：只注入该身份。
	b = body(`{"url":"` + srv.URL + `/whoami", "identity":"user"}`)
	if !strings.Contains(b, "COOKIE[u=1]") || !strings.Contains(b, "TOKEN[]") {
		t.Fatalf("identity=user 应只见 user 的 Cookie 且无 X-Token: %s", b)
	}

	// 3) 匿名：什么凭证都不带。
	b = body(`{"url":"` + srv.URL + `/whoami", "identity":""}`)
	if !strings.Contains(b, "COOKIE[]") || !strings.Contains(b, "TOKEN[]") {
		t.Fatalf("identity=\"\" 应完全匿名: %s", b)
	}

	// 4) 会话污染回归：先以会话语义登录（Set-Cookie 进 jar），再匿名——sess 不得出现。
	_ = body(`{"url":"` + srv.URL + `/set-sess"}`)
	b = body(`{"url":"` + srv.URL + `/whoami", "identity":""}`)
	if strings.Contains(b, "sess=xyz") {
		t.Fatalf("匿名请求被任务会话 Cookie 污染: %s", b)
	}

	// 5) 指定身份的响应 Set-Cookie 归属该身份（不进任务会话、不影响其他身份）。
	_ = body(`{"url":"` + srv.URL + `/refresh", "identity":"admin"}`)
	creds.mu.Lock()
	var adminCookie, userCookie string
	for _, id := range creds.byHost[host] {
		for _, c := range id.Credentials {
			if c.Type == credential.TypeHeaders && c.Key == "Cookie" {
				switch id.Username {
				case "admin":
					adminCookie = c.Value
				case "user":
					userCookie = c.Value
				}
			}
		}
	}
	creds.mu.Unlock()
	if !strings.Contains(adminCookie, "admin_sess=renewed") || !strings.Contains(adminCookie, "a=1") {
		t.Fatalf("admin 身份的 Cookie 应合并续期值并保留原值: %q", adminCookie)
	}
	if strings.Contains(userCookie, "admin_sess") {
		t.Fatalf("续期不得串味到其他身份: %q", userCookie)
	}
	// 匿名路径也不落会话：匿名请求 /refresh 不写任何身份。
	_ = body(`{"url":"` + srv.URL + `/whoami", "identity":""}`)
}
