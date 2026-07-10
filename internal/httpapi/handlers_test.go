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

	"github.com/V3teran/liusha/internal/credential"
)

// fakeCred 是 CredentialsAPI 的内存实现，用于路由级单测。
type fakeCred struct {
	saved   map[string][]credential.Identity
	listed  map[string][]credential.Identity
	deleted []string
	lastTTL int
}

func (f *fakeCred) BatchSave(_ context.Context, byHost map[string][]credential.Identity, ttl int) error {
	if f.saved == nil {
		f.saved = map[string][]credential.Identity{}
	}
	for k, v := range byHost {
		f.saved[k] = v
	}
	f.lastTTL = ttl
	return nil
}

func (f *fakeCred) GetIdentitiesByHost(_ context.Context, host string) ([]credential.Identity, error) {
	return f.listed[host], nil
}

func (f *fakeCred) Delete(_ context.Context, host string) error {
	f.deleted = append(f.deleted, host)
	return nil
}

// fakeAbort 是 OwnersAPI 的内存实现。
type fakeAbort struct {
	aborted []string
}

func (f *fakeAbort) Abort(_ context.Context, id string) error {
	f.aborted = append(f.aborted, id)
	return nil
}

// List 简单 mock：返回固定 1 条 stub summary，足以让现有测试通过 typecheck；
// 真正的 List handler 行为校验留给将来按需补 TestListSessions_*。
func (f *fakeAbort) List(_ context.Context, _ int) ([]OwnerSummary, error) {
	return []OwnerSummary{{ID: "stub-eid", Scope: `{"any":true}`, Status: "active"}}, nil
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
	defer resp.Body.Close()

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

	resp, err := http.Get(srv.URL + "/credential?host=h")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestAuth_RejectsWrongKey(t *testing.T) {
	srv := newTestServer(t, Deps{Credentials: &fakeCred{}})
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/credential?host=h", nil)
	req.Header.Set("X-API-Key", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestCredentialBatch(t *testing.T) {
	fc := &fakeCred{}
	srv := newTestServer(t, Deps{Credentials: fc})
	defer srv.Close()

	body, _ := json.Marshal(BatchSaveRequest{
		TTLSeconds: 60,
		Credentials: map[string][]credential.Identity{
			"vulnapp": {{Name: "admin", Role: "admin"}},
			"shop":    {{Name: "user", Role: "user"}},
		},
	})
	req, _ := http.NewRequest("POST", srv.URL+"/credential/batch", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "k")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(b))
	}
	if _, ok := fc.saved["vulnapp"]; !ok {
		t.Fatalf("vulnapp not saved: %#v", fc.saved)
	}
	if _, ok := fc.saved["shop"]; !ok {
		t.Fatalf("shop not saved: %#v", fc.saved)
	}
	if fc.lastTTL != 60 {
		t.Fatalf("ttl=%d", fc.lastTTL)
	}
}

func TestCredentialBatch_BadJSON(t *testing.T) {
	srv := newTestServer(t, Deps{Credentials: &fakeCred{}})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/credential/batch", bytes.NewReader([]byte("not json")))
	req.Header.Set("X-API-Key", "k")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestCredentialList(t *testing.T) {
	fc := &fakeCred{
		listed: map[string][]credential.Identity{
			"vulnapp": {{Name: "anonymous"}, {Name: "admin", Role: "admin"}},
		},
	}
	srv := newTestServer(t, Deps{Credentials: fc})
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/credential?host=vulnapp", nil)
	req.Header.Set("X-API-Key", "k")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	var body struct {
		Identities []credential.Identity `json:"identities"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Identities) != 2 {
		t.Fatalf("identities=%v", body.Identities)
	}
}

func TestCredentialList_MissingHost(t *testing.T) {
	srv := newTestServer(t, Deps{Credentials: &fakeCred{}})
	defer srv.Close()

	req, _ := http.NewRequest("GET", srv.URL+"/credential", nil)
	req.Header.Set("X-API-Key", "k")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestCredentialDelete(t *testing.T) {
	fc := &fakeCred{}
	srv := newTestServer(t, Deps{Credentials: fc})
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/credential?host=vulnapp", nil)
	req.Header.Set("X-API-Key", "k")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(fc.deleted) != 1 || fc.deleted[0] != "vulnapp" {
		t.Fatalf("deleted=%v", fc.deleted)
	}
}

func TestSessionAbort(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Owners: fa})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/session/eid-1/abort", nil)
	req.Header.Set("X-API-Key", "k")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(fa.aborted) != 1 || fa.aborted[0] != "eid-1" {
		t.Fatalf("aborted=%v", fa.aborted)
	}
}

func TestSessionAbort_RequiresAuth(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Owners: fa})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/session/eid-1/abort", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(fa.aborted) != 0 {
		t.Fatalf("should not have called Abort: %v", fa.aborted)
	}
}

// 删除 TestPassiveScan_Created / _RequiresAuth / _LookupError 三个用例：
// owner 多态坍缩为统一 task 后，OwnersAPI.EnsurePassiveSession 及 POST /scan/passive 路由已移除
// （passive task 由 ingestor 按流量窗口聚合生成，不再走 API 预热路径）。

// fakeActiveScan 是 ActiveScanAPI 的内存实现：记录最近一次 CreateActiveScan 入参，可注入 err。
type fakeActiveScan struct {
	gotBrief            string
	calls               int
	err                 error
	retEID, retHunterID string
}

func (f *fakeActiveScan) CreateActiveScan(_ context.Context, brief string) (string, string, error) {
	f.calls++
	f.gotBrief = brief
	if f.err != nil {
		return "", "", f.err
	}
	eid := f.retEID
	if eid == "" {
		eid = "eid-active"
	}
	tid := f.retHunterID
	if tid == "" {
		tid = "task-active"
	}
	return eid, tid, nil
}

// TestActiveScan_Created：正常路径 → 200 + {owner_id, hunter_id}；fake 记录 brief 原文。
func TestActiveScan_Created(t *testing.T) {
	fs := &fakeActiveScan{}
	srv := newTestServer(t, Deps{ActiveScan: fs})
	defer srv.Close()

	brief := "测试网站 http://111.229.193.40:34280/login.php，账号 admin/password，只测 XSS"
	body, _ := json.Marshal(CreateActiveScanRequest{Brief: brief})
	req, _ := http.NewRequest("POST", srv.URL+"/scan/active", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "k")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("status=%d body=%s", resp.StatusCode, string(b))
	}
	var out struct {
		OwnerID  string `json:"owner_id"`
		HunterID string `json:"hunter_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.OwnerID != "eid-active" || out.HunterID != "task-active" {
		t.Fatalf("ids: oid=%q tid=%q", out.OwnerID, out.HunterID)
	}
	if fs.calls != 1 {
		t.Fatalf("calls=%d, want 1", fs.calls)
	}
	if fs.gotBrief != brief {
		t.Fatalf("gotBrief mismatch: got=%q want=%q", fs.gotBrief, brief)
	}
}

// TestActiveScan_MissingBrief：brief 缺失或全空白 → 400。
func TestActiveScan_MissingBrief(t *testing.T) {
	cases := []string{"", "   "}
	for _, b := range cases {
		fs := &fakeActiveScan{}
		srv := newTestServer(t, Deps{ActiveScan: fs})

		body, _ := json.Marshal(CreateActiveScanRequest{Brief: b})
		req, _ := http.NewRequest("POST", srv.URL+"/scan/active", bytes.NewReader(body))
		req.Header.Set("X-API-Key", "k")
		req.Header.Set("Content-Type", "application/json")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("brief=%q do: %v", b, err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Fatalf("brief=%q status=%d, want 400", b, resp.StatusCode)
		}
		if fs.calls != 0 {
			t.Fatalf("brief=%q 不应调底层: calls=%d", b, fs.calls)
		}
		srv.Close()
	}
}

// TestActiveScan_RequiresAuth：缺 X-API-Key → 401。
func TestActiveScan_RequiresAuth(t *testing.T) {
	fs := &fakeActiveScan{}
	srv := newTestServer(t, Deps{ActiveScan: fs})
	defer srv.Close()

	body, _ := json.Marshal(CreateActiveScanRequest{Brief: "测试 https://x.com"})
	req, _ := http.NewRequest("POST", srv.URL+"/scan/active", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if fs.calls != 0 {
		t.Fatalf("不应调底层: calls=%d", fs.calls)
	}
}

// TestActiveScan_BackendError：底层报错 → 500。
func TestActiveScan_BackendError(t *testing.T) {
	fs := &fakeActiveScan{err: errors.New("db boom")}
	srv := newTestServer(t, Deps{ActiveScan: fs})
	defer srv.Close()

	body, _ := json.Marshal(CreateActiveScanRequest{Brief: "测试 https://x.com"})
	req, _ := http.NewRequest("POST", srv.URL+"/scan/active", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "k")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 500 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}
