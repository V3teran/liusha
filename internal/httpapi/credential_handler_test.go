package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/credential"
)

// credentialTestEnv 是凭证库路由级测试的公共装配。
type credentialTestEnv struct {
	srv     *httptest.Server
	fake    *fakeCred
	auditEv *fakeAudit
	status  fakeTaskStatus
}

func newCredentialTestServer(t *testing.T, seed map[string][]credential.Identity, statuses fakeTaskStatus, withAudit bool) *credentialTestEnv {
	t.Helper()
	fc := &fakeCred{byHost: seed}
	env := &credentialTestEnv{fake: fc, status: statuses}
	d := Deps{Credentials: fc, TaskStatus: statuses}
	if withAudit {
		env.auditEv = &fakeAudit{}
		d.CredentialAudit = env.auditEv
	}
	env.srv = newTestServer(t, d)
	t.Cleanup(func() { env.srv.Close() })
	return env
}

func doReq(t *testing.T, method, url, body string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != "" {
		reader = strings.NewReader(body)
	}
	req, _ := http.NewRequest(method, url, reader)
	req.AddCookie(testSessionCookie())
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode: %v", err)
	}
}

// listCredentials 请求 GET /api/credentials 并解码分页响应。
func listCredentials(t *testing.T, env *credentialTestEnv, query string) (items []credentialRowView, total int) {
	t.Helper()
	resp := doReq(t, "GET", env.srv.URL+"/api/credentials"+query, "")
	if resp.StatusCode != 200 {
		t.Fatalf("GET /credentials%s status=%d", query, resp.StatusCode)
	}
	var body struct {
		Items []credentialRowView `json:"items"`
		Total int                 `json:"total"`
	}
	decode(t, resp, &body)
	return body.Items, body.Total
}

func at(minute int) *time.Time {
	t := time.Date(2026, 11, 21, 10, minute, 0, 0, time.UTC)
	return &t
}

func cookie(v string) []credential.Credential {
	return []credential.Credential{{Type: credential.TypeHeaders, Key: "Cookie", Value: v}}
}

// TestCredentialList_Plaintext 锁定明文口径：凭证值原样返回（操作台要看 / 要复制原文），
// 每行带上所在 host；读取不落审计（改必审计、读不审计）。
func TestCredentialList_Plaintext(t *testing.T) {
	long := "PHPSESSID=abcdef0123456789; security=low"
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {
			{Username: "admin", Credentials: []credential.Credential{
				{Type: credential.TypeHeaders, Key: "Cookie", Value: long},
				{Type: credential.TypeHeaders, Key: "X-Short", Value: "s3cret"},
			}},
		},
	}, nil, true)

	items, total := listCredentials(t, env, "")
	if total != 1 || len(items) != 1 || items[0].Host != "a.com" {
		t.Fatalf("total=%d items=%+v", total, items)
	}
	creds := items[0].Credentials
	if creds[0].Value != long || creds[1].Value != "s3cret" {
		t.Fatalf("值应原样返回: %+v", creds)
	}
	if len(env.auditEv.events) != 0 {
		t.Fatal("读取不应落审计（改必审计、读不审计）")
	}
}

// TestCredentialList_SessionEnrichment 锁定会话行的任务状态注入：active/completed 如实标注，
// 任务行已删标 gone；没有凭证的空壳会话身份不进列表。
func TestCredentialList_SessionEnrichment(t *testing.T) {
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {
			{Username: credential.SessionIdentityName("t-live"), Credentials: cookie("l")},
			{Username: credential.SessionIdentityName("t-done"), Credentials: cookie("d")},
			{Username: credential.SessionIdentityName("t-gone"), Credentials: cookie("g")},
			{Username: credential.SessionIdentityName("t-empty")},
			{Username: "admin", Credentials: cookie("a")},
		},
	}, fakeTaskStatus{"t-live": "active", "t-done": "completed"}, false)

	items, total := listCredentials(t, env, "")
	if total != 4 {
		t.Fatalf("空壳会话身份不应计入: total=%d", total)
	}
	byName := map[string]credentialRowView{}
	for _, v := range items {
		byName[v.Username] = v
	}
	if v := byName["task:t-live:session"]; v.Kind != "session" || v.TaskID != "t-live" || v.TaskStatus != "active" {
		t.Fatalf("live 会话视图: %+v", v)
	}
	if v := byName["task:t-done:session"]; v.TaskStatus != "completed" {
		t.Fatalf("done 会话视图: %+v", v)
	}
	if v := byName["task:t-gone:session"]; v.TaskStatus != "gone" {
		t.Fatalf("gone 会话视图: %+v", v)
	}
	if v := byName["admin"]; v.Kind != "persistent" || v.TaskStatus != "" {
		t.Fatalf("持久身份视图: %+v", v)
	}
}

// TestCredentialList_SearchPageAndOrder 锁定服务端检索与分页：关键词跨 host / 用户名 / 角色 /
// 凭证键值不区分大小写匹配，总数是匹配总数；按录入时间倒序，缺录入时间的排最后。
func TestCredentialList_SearchPageAndOrder(t *testing.T) {
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {
			{Username: "admin", Role: "admin", Credentials: cookie("tok-a"), CreatedAt: at(1)},
			{Username: "guest", Role: "user", Credentials: cookie("guest-TK"), CreatedAt: at(3)},
		},
		"b.com:8080": {
			{Username: "ops", Role: "user", Credentials: cookie("tok-b"), CreatedAt: at(2)},
			{Username: "legacy", Role: "user", Credentials: cookie("old")},
		},
	}, nil, false)

	items, total := listCredentials(t, env, "")
	got := []string{}
	for _, v := range items {
		got = append(got, v.Host+"/"+v.Username)
	}
	want := []string{"a.com/guest", "b.com:8080/ops", "a.com/admin", "b.com:8080/legacy"}
	if total != 4 || strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("顺序 got=%v want=%v total=%d", got, want, total)
	}

	cases := []struct {
		query string
		names []string
		total int
	}{
		{"?search=guest-tk", []string{"guest"}, 1},            // 凭证值，不区分大小写
		{"?search=B.COM", []string{"ops", "legacy"}, 2},       // host
		{"?search=cookie%3Dtok", []string{"ops", "admin"}, 2}, // 键=值
		{"?search=%20admin%20", []string{"admin"}, 1},         // 首尾空白忽略；用户名 / 角色
		{"?size=2&page=2", []string{"admin", "legacy"}, 4},    // 第二页，总数不变
		{"?size=2&page=9", []string{}, 4},                     // 越界页为空
	}
	for _, tc := range cases {
		items, total := listCredentials(t, env, tc.query)
		names := []string{}
		for _, v := range items {
			names = append(names, v.Username)
		}
		if total != tc.total || strings.Join(names, ",") != strings.Join(tc.names, ",") {
			t.Errorf("%s: names=%v total=%d, want %v total=%d", tc.query, names, total, tc.names, tc.total)
		}
	}
}

// TestCredentialList_ReadFailure 锁定故障口径：任一 host 读取失败即整体 500，
// 不把「读不到」伪装成「没有」的残缺列表。
func TestCredentialList_ReadFailure(t *testing.T) {
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {{Username: "admin", Credentials: cookie("a")}},
		"b.com": {{Username: "ops", Credentials: cookie("b")}},
	}, nil, false)
	env.fake.failHost = "b.com"
	resp := doReq(t, "GET", env.srv.URL+"/api/credentials", "")
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", resp.StatusCode)
	}
}

// TestCredentialUpsert_ValidationAndHostNormalization 锁定写入校验与
// host 归一（带端口/带 scheme 的 host 归一到注册主机）。
func TestCredentialUpsert_ValidationAndHostNormalization(t *testing.T) {
	env := newCredentialTestServer(t, nil, nil, true)

	ok := `{"username":"admin","role":"admin","credentials":[{"type":"headers","key":"Cookie","value":"sid=1"}]}`
	for _, tc := range []struct {
		name, body string
		code       int
	}{
		{"合法写入", ok, 200},
		{"缺用户名", `{"username":"","credentials":[{"type":"headers","key":"k","value":"v"}]}`, 400},
		{"会话保留名", strings.Replace(ok, `"admin"`, `"task:t1:session"`, 1), 400},
		{"空凭证数组", `{"username":"x","credentials":[]}`, 400},
		{"凭证缺 value", `{"username":"x","credentials":[{"type":"headers","key":"k","value":""}]}`, 400},
		{"凭证类型非法", `{"username":"x","credentials":[{"type":"cookie","key":"k","value":"v"}]}`, 400},
	} {
		resp := doReq(t, "PUT", env.srv.URL+"/api/credentials/a.com:8080/usernames", tc.body)
		_ = resp.Body.Close()
		if resp.StatusCode != tc.code {
			t.Fatalf("%s: status=%d 期望 %d", tc.name, resp.StatusCode, tc.code)
		}
	}

	ids, _ := env.fake.GetIdentitiesByHost(context.Background(), "a.com:8080")
	if len(ids) != 1 || ids[0].Username != "admin" {
		t.Fatalf("host 未归一（应含端口）或未落库: %+v", ids)
	}
	// 身份载荷不冗余存地址：主机展示统一取存储键（CanonicalHost 归一结果）。
	if ids[0].Role != "admin" {
		t.Fatalf("role 未落库: %+v", ids[0])
	}
	if len(env.auditEv.events) != 1 || env.auditEv.events[0].Action != audit.ActionCredentialSet {
		t.Fatalf("写入应落审计: %+v", env.auditEv.events)
	}
}

// TestCredentialDelete_MissingIdentity 锁定删除不存在的身份：404，且不落审计——审计里不能出现没发生过的删除。
func TestCredentialDelete_MissingIdentity(t *testing.T) {
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {{Username: "admin"}},
	}, nil, true)

	resp := doReq(t, "DELETE", env.srv.URL+"/api/credentials/a.com/usernames/ghost", "")
	_ = resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("删除不存在的身份应 404，得 %d", resp.StatusCode)
	}
	if len(env.auditEv.events) != 0 {
		t.Fatalf("没删掉任何东西不应落审计: %+v", env.auditEv.events)
	}
}

// TestCredentialDelete_Identity 锁定单身份删除：只删指定身份并落审计。
func TestCredentialDelete_Identity(t *testing.T) {
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {{Username: "admin"}, {Username: credential.SessionIdentityName("t1")}},
	}, nil, true)

	resp := doReq(t, "DELETE", env.srv.URL+"/api/credentials/a.com/usernames/task:t1:session", "")
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("单删 status=%d", resp.StatusCode)
	}
	if len(env.auditEv.events) != 1 {
		t.Fatalf("删除操作应落审计: %+v", env.auditEv.events)
	}
}

// 录入非法主机 400，且什么都不写：凭证按主机键匹配目标，非法主机的条目永远用不上。
func TestCredentialPut_InvalidHost(t *testing.T) {
	env := newCredentialTestServer(t, map[string][]credential.Identity{}, nil, true)
	body := `{"username":"alice","credentials":[{"type":"headers","key":"Cookie","value":"v"}]}`
	for _, h := range []string{"bad%20host!", "host:99999"} {
		resp := doReq(t, "PUT", env.srv.URL+"/api/credentials/"+h+"/usernames", body)
		_ = resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s 应 400，得 %d", h, resp.StatusCode)
		}
	}
	if len(env.fake.byHost) != 0 || len(env.auditEv.events) != 0 {
		t.Fatalf("非法主机不应写入或落审计: %+v %+v", env.fake.byHost, env.auditEv.events)
	}
}
