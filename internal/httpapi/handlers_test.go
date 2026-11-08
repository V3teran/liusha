package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/task"
)

// fakeCred 是 CredentialsAPI 的内存实现，用于路由级单测。
type fakeCred struct {
	byHost         map[string][]credential.Identity
	deletedIDs     []string
	deletedHosts   []string
	purgedSessions int
}

func (f *fakeCred) SaveIdentities(_ context.Context, host string, ids []credential.Identity) error {
	if f.byHost == nil {
		f.byHost = map[string][]credential.Identity{}
	}
	for _, id := range ids {
		replaced := false
		for i, ex := range f.byHost[host] {
			if ex.Username == id.Username {
				f.byHost[host][i] = id
				replaced = true
			}
		}
		if !replaced {
			f.byHost[host] = append(f.byHost[host], id)
		}
	}
	return nil
}

func (f *fakeCred) GetIdentitiesByHost(_ context.Context, host string) ([]credential.Identity, error) {
	src := f.byHost[host]
	out := make([]credential.Identity, len(src))
	for i, id := range src {
		// 深拷贝凭证数组：调用方（handler 脱敏）原地改写不得渗回存储——
		// 真实 Redis 每次读都是新反序列化值，fake 必须同语义。
		creds := make([]credential.Credential, len(id.Credentials))
		copy(creds, id.Credentials)
		id.Credentials = creds
		out[i] = id
	}
	return out, nil
}

func (f *fakeCred) DeleteIdentity(_ context.Context, host, name string) error {
	f.deletedIDs = append(f.deletedIDs, credential.CanonicalHost(host)+"/"+name)
	kept := f.byHost[host][:0]
	for _, id := range f.byHost[host] {
		if id.Username != name {
			kept = append(kept, id)
		}
	}
	f.byHost[host] = kept
	return nil
}

func (f *fakeCred) DeleteSessions(_ context.Context, host string) (int, error) {
	n := 0
	for _, id := range append([]credential.Identity{}, f.byHost[host]...) {
		if credential.IsSessionIdentity(id.Username) {
			_ = f.DeleteIdentity(context.Background(), host, id.Username)
			n++
		}
	}
	f.purgedSessions = n
	return n, nil
}

func (f *fakeCred) DeleteHost(_ context.Context, host string) error {
	f.deletedHosts = append(f.deletedHosts, credential.CanonicalHost(host))
	delete(f.byHost, host)
	return nil
}

func (f *fakeCred) ListHosts(_ context.Context) ([]credential.HostSummary, error) {
	var out []credential.HostSummary
	for host, ids := range f.byHost {
		s := credential.HostSummary{Host: host}
		for _, id := range ids {
			if credential.IsSessionIdentity(id.Username) {
				s.Sessions++
			} else {
				s.Usernames++
			}
		}
		if s.Usernames+s.Sessions > 0 {
			out = append(out, s)
		}
	}
	return out, nil
}

// fakeAudit 收集审计事件供断言。
type fakeAudit struct{ events []audit.Event }

func (f *fakeAudit) Append(_ context.Context, e audit.Event) (int64, error) {
	f.events = append(f.events, e)
	return int64(len(f.events)), nil
}

// fakeTaskStatus 是 TaskStatusResolver 的表替身。
type fakeTaskStatus map[string]string

func (f fakeTaskStatus) TaskStatus(_ context.Context, taskID string) (string, bool) {
	st, ok := f[taskID]
	return st, ok
}

func newTestServer(t *testing.T, d Deps) *httptest.Server {
	t.Helper()
	if d.Auth == nil {
		d.Auth = newFakeAuthStore()
	}
	return httptest.NewServer(NewServer(d))
}

func TestHealthz_NoAuth(t *testing.T) {
	srv := newTestServer(t, Deps{})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["ok"] != true {
		t.Fatalf("body=%v", body)
	}
}

func TestAuth_RejectsMissingHeader(t *testing.T) {
	srv := newTestServer(t, Deps{Credentials: &fakeCred{}})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/credentials/h")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestAuth_RejectsWrongSession(t *testing.T) {
	srv := newTestServer(t, Deps{Credentials: &fakeCred{}})
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/api/credentials/h", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "forged-token"}) // #nosec G124 // 测试伪造凭证，非真实会话
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

// fakeTaskControl 是 TaskControlAPI 的表替身：按 action 记录调用并返回预设错误。
type fakeTaskControl struct {
	err    error
	called []string
}

func (f *fakeTaskControl) PauseTask(_ context.Context, _ string) error {
	f.called = append(f.called, "pause")
	return f.err
}

func (f *fakeTaskControl) ResumeTask(_ context.Context, _ string) error {
	f.called = append(f.called, "resume")
	return f.err
}

func (f *fakeTaskControl) TerminateTask(_ context.Context, _ string) error {
	f.called = append(f.called, "terminate")
	return f.err
}

func TestTaskControl_StateConflictMaps409(t *testing.T) {
	fake := &fakeTaskControl{err: task.ErrNotActive}
	srv := newTestServer(t, Deps{TaskControl: fake})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/api/tasks/00000000-0000-0000-0000-000000000000/control",
		strings.NewReader(`{"action":"pause"}`))
	req.AddCookie(testSessionCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("终态任务的 pause 应映射 409，实得 %d", resp.StatusCode)
	}
}

func TestTaskControl_OK(t *testing.T) {
	fake := &fakeTaskControl{}
	srv := newTestServer(t, Deps{TaskControl: fake})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/api/tasks/00000000-0000-0000-0000-000000000000/control",
		strings.NewReader(`{"action":"resume"}`))
	req.AddCookie(testSessionCookie())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(fake.called) != 1 || fake.called[0] != "resume" {
		t.Fatalf("called=%v", fake.called)
	}
}
