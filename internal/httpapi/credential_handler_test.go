package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	req.Header.Set("X-API-Key", "k")
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

// TestCredentialHosts_List 锁定枚举：host 汇总 + 身份分类计数。
func TestCredentialHosts_List(t *testing.T) {
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {{Username: "admin"}, {Username: credential.SessionIdentityName("t1")}},
	}, nil, false)

	var body struct {
		Hosts []credential.HostSummary `json:"hosts"`
	}
	resp := doReq(t, "GET", env.srv.URL+"/credentials", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	decode(t, resp, &body)
	if len(body.Hosts) != 1 || body.Hosts[0].Usernames != 1 || body.Hosts[0].Sessions != 1 {
		t.Fatalf("hosts=%+v", body.Hosts)
	}
}

// TestCredentialDetail_Plaintext 锁定明文口径：凭证值原样返回（操作台要
// 看/要复制原文），读取不落审计（改必审计、读不审计）。
func TestCredentialDetail_Plaintext(t *testing.T) {
	long := "PHPSESSID=abcdef0123456789; security=low"
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {
			{Username: "admin", Credentials: []credential.Credential{
				{Type: credential.TypeHeaders, Key: "Cookie", Value: long},
				{Type: credential.TypeHeaders, Key: "X-Short", Value: "s3cret"},
			}},
		},
	}, nil, true)

	var body struct {
		Identities []credentialIdentityView `json:"identities"`
	}
	resp := doReq(t, "GET", env.srv.URL+"/credentials/a.com", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	decode(t, resp, &body)
	creds := body.Identities[0].Credentials
	if creds[0].Value != long || creds[1].Value != "s3cret" {
		t.Fatalf("值应原样返回: %+v", creds)
	}
	if len(env.auditEv.events) != 0 {
		t.Fatal("读取不应落审计（改必审计、读不审计）")
	}
}

// TestCredentialDetail_SessionEnrichment 锁定会话行的任务状态注入：
// active/completed 如实标注，任务行已删标 gone。
func TestCredentialDetail_SessionEnrichment(t *testing.T) {
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {
			{Username: credential.SessionIdentityName("t-live")},
			{Username: credential.SessionIdentityName("t-done")},
			{Username: credential.SessionIdentityName("t-gone")},
			{Username: "admin"},
		},
	}, fakeTaskStatus{"t-live": "active", "t-done": "completed"}, false)

	var body struct {
		Identities []credentialIdentityView `json:"identities"`
	}
	resp := doReq(t, "GET", env.srv.URL+"/credentials/a.com", "")
	decode(t, resp, &body)
	byName := map[string]credentialIdentityView{}
	for _, v := range body.Identities {
		byName[v.Username] = v
	}
	if v := byName["task:t-live:session"]; v.Kind != "session" || v.TaskStatus != "active" {
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
		resp := doReq(t, "PUT", env.srv.URL+"/credentials/a.com:8080/usernames", tc.body)
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

// TestCredentialDelete_Kinds 锁定三条删除通道各自的作用域。
func TestCredentialDelete_Kinds(t *testing.T) {
	env := newCredentialTestServer(t, map[string][]credential.Identity{
		"a.com": {{Username: "admin"}, {Username: credential.SessionIdentityName("t1")}},
	}, nil, true)

	resp := doReq(t, "DELETE", env.srv.URL+"/credentials/a.com/usernames/task:t1:session", "")
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("单删 status=%d", resp.StatusCode)
	}
	resp = doReq(t, "DELETE", env.srv.URL+"/credentials/a.com/sessions", "")
	var purged struct{ Deleted int }
	decode(t, resp, &purged)
	if purged.Deleted != 0 {
		t.Fatalf("已单删后无会话可清: %+v", purged)
	}
	resp = doReq(t, "DELETE", env.srv.URL+"/credentials/a.com", "")
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || len(env.fake.deletedHosts) != 1 || env.fake.deletedHosts[0] != "a.com" {
		t.Fatalf("整删: status=%d hosts=%v", resp.StatusCode, env.fake.deletedHosts)
	}
	if len(env.auditEv.events) != 3 {
		t.Fatalf("全部删除操作应落审计: %+v", env.auditEv.events)
	}
}
