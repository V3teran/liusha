package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/V3teran/liusha/internal/audit"
	"github.com/V3teran/liusha/internal/credential"
)

// fakeCred 是 CredentialsAPI 的内存实现，用于路由级单测。
type fakeCred struct {
	byHost         map[string][]credential.Identity
	deletedIds     []string
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
	f.deletedIds = append(f.deletedIds, credential.CanonicalHost(host)+"/"+name)
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

// fakeAbort 是 TaskAPI 的内存实现。
type fakeAbort struct {
	aborted []string
}

func (f *fakeAbort) Abort(_ context.Context, id string) error {
	f.aborted = append(f.aborted, id)
	return nil
}

// List 简单 mock：返回固定 1 条 stub summary，足以让现有测试通过 typecheck；
// 真正的 List handler 行为校验留给将来按需补 TestListSessions_*。
func (f *fakeAbort) List(_ context.Context, _ int) ([]TaskSummary, error) {
	return []TaskSummary{{ID: "stub-eid", Scope: `{"any":true}`, Status: "active"}}, nil
}

func newTestServer(t *testing.T, d Deps) *httptest.Server {
	t.Helper()
	if d.APIKey == "" {
		d.APIKey = "k"
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

	resp, err := http.Get(srv.URL + "/credentials/h")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestAuth_RejectsWrongKey(t *testing.T) {
	srv := newTestServer(t, Deps{Credentials: &fakeCred{}})
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/credentials/h", nil)
	req.Header.Set("X-API-Key", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestTaskAbort(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Tasks: fa})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/tasks/eid-1/abort", nil)
	req.Header.Set("X-API-Key", "k")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(fa.aborted) != 1 || fa.aborted[0] != "eid-1" {
		t.Fatalf("aborted=%v", fa.aborted)
	}
}

func TestTaskAbort_RequiresAuth(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Tasks: fa})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/tasks/eid-1/abort", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(fa.aborted) != 0 {
		t.Fatalf("should not have called Abort: %v", fa.aborted)
	}
}

// 删除 TestPassiveScan_Created / _RequiresAuth / _LookupError 三个用例：
// owner 多态坍缩为统一 task 后，TaskAPI.EnsurePassiveSession 及 POST /scan/passive 路由已移除
// （passive task 由 ingestor 按流量窗口聚合生成，不再走 API 预热路径）。

// fakeScan 是 ScanAPI 的内存实现：记录最近一次 CreateScan 入参，可注入 err。
type fakeScan struct {
	gotBrief                 string
	calls                    int
	err                      error
	retTaskID, retAgentRunID string
}

func (f *fakeScan) CreateScan(_ context.Context, brief string) (string, string, error) {
	f.calls++
	f.gotBrief = brief
	if f.err != nil {
		return "", "", f.err
	}
	taskID := f.retTaskID
	if taskID == "" {
		taskID = "task-id"
	}
	tid := f.retAgentRunID
	if tid == "" {
		tid = "agent-id"
	}
	return taskID, tid, nil
}

// TestScan_Created：正常路径 → 200 + {task_id, agent_id}；fake 记录 brief 原文。
func TestScan_Created(t *testing.T) {
	fs := &fakeScan{}
	srv := newTestServer(t, Deps{Scan: fs})
	defer srv.Close()

	brief := "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password，只测 XSS"
	body, _ := json.Marshal(CreateScanRequest{Brief: brief})
	req, _ := http.NewRequest("POST", srv.URL+"/scan", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "k")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(b))
	}
	var out struct {
		TaskID     string `json:"task_id"`
		AgentRunID string `json:"agent_run_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.TaskID != "task-id" || out.AgentRunID != "agent-id" {
		t.Fatalf("ids: tid=%q run=%q", out.TaskID, out.AgentRunID)
	}
	if fs.calls != 1 {
		t.Fatalf("calls=%d, want 1", fs.calls)
	}
	if fs.gotBrief != brief {
		t.Fatalf("gotBrief mismatch: got=%q want=%q", fs.gotBrief, brief)
	}
}

// TestScan_MissingBrief：brief 缺失或全空白 → 400。
func TestScan_MissingBrief(t *testing.T) {
	cases := []string{"", "   "}
	for _, b := range cases {
		fs := &fakeScan{}
		srv := newTestServer(t, Deps{Scan: fs})

		body, _ := json.Marshal(CreateScanRequest{Brief: b})
		req, _ := http.NewRequest("POST", srv.URL+"/scan", bytes.NewReader(body))
		req.Header.Set("X-API-Key", "k")
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("brief=%q do: %v", b, err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 400 {
			t.Fatalf("brief=%q status=%d, want 400", b, resp.StatusCode)
		}
		if fs.calls != 0 {
			t.Fatalf("brief=%q 不应调底层: calls=%d", b, fs.calls)
		}
		srv.Close()
	}
}

// TestScan_RequiresAuth：缺 X-API-Key → 401。
func TestScan_RequiresAuth(t *testing.T) {
	fs := &fakeScan{}
	srv := newTestServer(t, Deps{Scan: fs})
	defer srv.Close()

	body, _ := json.Marshal(CreateScanRequest{Brief: "测试 https://x.com"})
	req, _ := http.NewRequest("POST", srv.URL+"/scan", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if fs.calls != 0 {
		t.Fatalf("不应调底层: calls=%d", fs.calls)
	}
}

// TestScan_BackendError：底层报错 → 500。
func TestScan_BackendError(t *testing.T) {
	fs := &fakeScan{err: errors.New("db boom")}
	srv := newTestServer(t, Deps{Scan: fs})
	defer srv.Close()

	body, _ := json.Marshal(CreateScanRequest{Brief: "测试 https://x.com"})
	req, _ := http.NewRequest("POST", srv.URL+"/scan", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "k")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
