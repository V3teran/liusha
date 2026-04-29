package replay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/V3teran/liusha/internal/credential"
)

// 使用 httptest.NewServer 验证替换后实际发出的请求内容。
func TestEngine_ReplayWithIdentity_HeadersSwap(t *testing.T) {
	var seenCookie string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenCookie = r.Header.Get("Cookie")
	}))
	defer srv.Close()

	raw := RawRequest{
		Method:  "GET",
		URL:     srv.URL + "/x",
		Headers: http.Header{"Cookie": {"session=admin_sess_a1b2c3"}},
	}
	id := credential.Identity{
		Name: "tester",
		Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "session=test_sess_d4e5f6"},
		},
	}

	resp, err := NewEngine(srv.Client()).ReplayWithIdentity(context.Background(), raw, id)
	if err != nil {
		t.Fatalf("replay err: %v", err)
	}
	if seenCookie != "session=test_sess_d4e5f6" {
		t.Fatalf("cookie not swapped: %q", seenCookie)
	}
	if resp.IdentityName != "tester" {
		t.Fatalf("IdentityName=%q", resp.IdentityName)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
}

func TestEngine_ReplayWithIdentity_QuerySwap(t *testing.T) {
	var seenURL string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenURL = r.URL.String()
	}))
	defer srv.Close()

	raw := RawRequest{
		Method:  "GET",
		URL:     srv.URL + "/api?uid=A&keep=1",
		Headers: http.Header{},
	}
	id := credential.Identity{
		Name: "u",
		Credentials: []credential.Credential{
			{Type: credential.TypeQuery, Key: "uid", Value: "B"},
		},
	}

	if _, err := NewEngine(srv.Client()).ReplayWithIdentity(context.Background(), raw, id); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenURL, "uid=B") {
		t.Fatalf("query not swapped: %q", seenURL)
	}
	if !strings.Contains(seenURL, "keep=1") {
		t.Fatalf("other query lost: %q", seenURL)
	}
}

func TestEngine_ReplayWithIdentity_BodyFormURLEncoded(t *testing.T) {
	var seenBody string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		seenBody = string(b)
	}))
	defer srv.Close()

	raw := RawRequest{
		Method:  "POST",
		URL:     srv.URL + "/login",
		Headers: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
		Body:    []byte("username=alice&password=x"),
	}
	id := credential.Identity{
		Name: "bob",
		Credentials: []credential.Credential{
			{Type: credential.TypeBody, Key: "username", Value: "bob"},
		},
	}

	if _, err := NewEngine(srv.Client()).ReplayWithIdentity(context.Background(), raw, id); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(seenBody, "username=bob") {
		t.Fatalf("body not swapped: %q", seenBody)
	}
	if !strings.Contains(seenBody, "password=x") {
		t.Fatalf("body other field lost: %q", seenBody)
	}
}

func TestEngine_ReplayWithIdentity_BodyJSON(t *testing.T) {
	var seenBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenBody, _ = io.ReadAll(r.Body)
	}))
	defer srv.Close()

	raw := RawRequest{
		Method:  "POST",
		URL:     srv.URL + "/api",
		Headers: http.Header{"Content-Type": {"application/json"}},
		Body:    []byte(`{"username":"alice","keep":"y"}`),
	}
	id := credential.Identity{
		Name: "carol",
		Credentials: []credential.Credential{
			{Type: credential.TypeBody, Key: "username", Value: "carol"},
		},
	}

	if _, err := NewEngine(srv.Client()).ReplayWithIdentity(context.Background(), raw, id); err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(seenBody, &parsed); err != nil {
		t.Fatalf("body not valid json: %v %q", err, seenBody)
	}
	if parsed["username"] != "carol" {
		t.Fatalf("json username not swapped: %v", parsed["username"])
	}
	if parsed["keep"] != "y" {
		t.Fatalf("other json field lost: %v", parsed["keep"])
	}
}

func TestEngine_ReplayWithIdentity_AnonymousNoOp(t *testing.T) {
	var seenAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seenAuth = r.Header.Get("Authorization")
	}))
	defer srv.Close()

	// 匿名身份不携带任何 Credential，且会清掉敏感头（Cookie / Authorization）。
	raw := RawRequest{
		Method:  "GET",
		URL:     srv.URL + "/",
		Headers: http.Header{"Authorization": {"Bearer xxx"}},
	}
	id := credential.Identity{Name: credential.AnonymousName}

	if _, err := NewEngine(srv.Client()).ReplayWithIdentity(context.Background(), raw, id); err != nil {
		t.Fatal(err)
	}
	if seenAuth != "" {
		t.Fatalf("anonymous should strip Authorization, got %q", seenAuth)
	}
}

func TestEngine_ReplayWithIdentity_DoesNotMutateInput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()

	originalHeaders := http.Header{"Cookie": {"session=old"}}
	originalBody := []byte("token=old")
	raw := RawRequest{
		Method:  "POST",
		URL:     srv.URL + "/?token=old",
		Headers: originalHeaders,
		Body:    originalBody,
	}
	id := credential.Identity{
		Name: "u",
		Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "session=new"},
			{Type: credential.TypeQuery, Key: "token", Value: "new"},
		},
	}

	if _, err := NewEngine(srv.Client()).ReplayWithIdentity(context.Background(), raw, id); err != nil {
		t.Fatal(err)
	}
	if got := originalHeaders.Get("Cookie"); got != "session=old" {
		t.Fatalf("input headers mutated: %q", got)
	}
	if string(originalBody) != "token=old" {
		t.Fatalf("input body mutated: %q", originalBody)
	}
	if raw.URL != srv.URL+"/?token=old" {
		t.Fatalf("input url mutated: %q", raw.URL)
	}
}

func TestEngine_ReplayMultiIdentity_Concurrency(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	raw := RawRequest{Method: "GET", URL: srv.URL + "/", Headers: http.Header{}}
	ids := []credential.Identity{{Name: "a"}, {Name: "b"}, {Name: "c"}}

	res, err := NewEngine(srv.Client()).ReplayMultiIdentity(context.Background(), raw, ids, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("len=%d", len(res))
	}
	// 顺序与 ids 一致。
	for i, want := range []string{"a", "b", "c"} {
		if res[i].IdentityName != want {
			t.Fatalf("res[%d]=%q want %q", i, res[i].IdentityName, want)
		}
	}
	if hits.Load() != 3 {
		t.Fatalf("hits=%d", hits.Load())
	}
}

func TestEngine_ReplayMultiIdentity_ZeroConcurrencyDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	raw := RawRequest{Method: "GET", URL: srv.URL + "/", Headers: http.Header{}}
	ids := []credential.Identity{{Name: "x"}, {Name: "y"}}

	// 0 / 负数应降级为内部默认值（不应死锁，不应报错）。
	res, err := NewEngine(srv.Client()).ReplayMultiIdentity(context.Background(), raw, ids, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("len=%d", len(res))
	}
}
