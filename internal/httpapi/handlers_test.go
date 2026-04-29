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

// fakeAbort 是 EngagementsAPI 的内存实现。
type fakeAbort struct {
	aborted []string

	// LookupOrCreateProxy 行为控制
	lookups   []string // 收到过的 host
	lookupErr error    // 非 nil 时返回错误
}

func (f *fakeAbort) Abort(_ context.Context, id string) error {
	f.aborted = append(f.aborted, id)
	return nil
}

// LookupOrCreateProxy 简单 mock：返回 "eid-"+host，便于断言幂等性。
func (f *fakeAbort) LookupOrCreateProxy(_ context.Context, host string) (string, error) {
	f.lookups = append(f.lookups, host)
	if f.lookupErr != nil {
		return "", f.lookupErr
	}
	return "eid-" + host, nil
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

func TestEngagementAbort(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Engagements: fa})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/engagement/eid-1/abort", nil)
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

func TestEngagementAbort_RequiresAuth(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Engagements: fa})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/engagement/eid-1/abort", nil)
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

// TestEngagementProxy_Created：POST /engagement/proxy 正常路径返回 engagement_id。
func TestEngagementProxy_Created(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Engagements: fa})
	defer srv.Close()

	body, _ := json.Marshal(CreateProxyRequest{Host: "vulnapp"})
	req, _ := http.NewRequest("POST", srv.URL+"/engagement/proxy", bytes.NewReader(body))
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
		EngagementID string `json:"engagement_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.EngagementID != "eid-vulnapp" {
		t.Fatalf("engagement_id=%q", out.EngagementID)
	}
	if len(fa.lookups) != 1 || fa.lookups[0] != "vulnapp" {
		t.Fatalf("lookups=%v", fa.lookups)
	}
}

// TestEngagementProxy_MissingHost：body 中无 host 应返回 400。
func TestEngagementProxy_MissingHost(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Engagements: fa})
	defer srv.Close()

	body, _ := json.Marshal(CreateProxyRequest{Host: ""})
	req, _ := http.NewRequest("POST", srv.URL+"/engagement/proxy", bytes.NewReader(body))
	req.Header.Set("X-API-Key", "k")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(fa.lookups) != 0 {
		t.Fatalf("should not have called LookupOrCreateProxy: %v", fa.lookups)
	}
}

// TestEngagementProxy_BadJSON：非法 JSON 应返回 400。
func TestEngagementProxy_BadJSON(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Engagements: fa})
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/engagement/proxy", bytes.NewReader([]byte("not json")))
	req.Header.Set("X-API-Key", "k")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(fa.lookups) != 0 {
		t.Fatalf("should not have called LookupOrCreateProxy: %v", fa.lookups)
	}
}

// TestEngagementProxy_RequiresAuth：缺 X-API-Key 应返回 401 且不调底层。
func TestEngagementProxy_RequiresAuth(t *testing.T) {
	fa := &fakeAbort{}
	srv := newTestServer(t, Deps{Engagements: fa})
	defer srv.Close()

	body, _ := json.Marshal(CreateProxyRequest{Host: "vulnapp"})
	req, _ := http.NewRequest("POST", srv.URL+"/engagement/proxy", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if len(fa.lookups) != 0 {
		t.Fatalf("should not have called LookupOrCreateProxy: %v", fa.lookups)
	}
}

// TestEngagementProxy_LookupError：底层报错应返回 500。
func TestEngagementProxy_LookupError(t *testing.T) {
	fa := &fakeAbort{lookupErr: errors.New("db boom")}
	srv := newTestServer(t, Deps{Engagements: fa})
	defer srv.Close()

	body, _ := json.Marshal(CreateProxyRequest{Host: "vulnapp"})
	req, _ := http.NewRequest("POST", srv.URL+"/engagement/proxy", bytes.NewReader(body))
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
