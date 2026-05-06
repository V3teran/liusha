package replay

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
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

func TestEngine_ReplayMatrix_BaselineConcurrency(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	raw := RawRequest{Method: "GET", URL: srv.URL + "/", Headers: http.Header{}}
	ids := []credential.Identity{{Name: "a"}, {Name: "b"}, {Name: "c"}}

	res, err := NewEngine(srv.Client()).ReplayMatrix(context.Background(), raw, ids, nil, 2)
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

func TestEngine_ReplayMatrix_ZeroConcurrencyDegrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	raw := RawRequest{Method: "GET", URL: srv.URL + "/", Headers: http.Header{}}
	ids := []credential.Identity{{Name: "x"}, {Name: "y"}}

	// 0 / 负数应降级为内部默认值（不应死锁，不应报错）。
	res, err := NewEngine(srv.Client()).ReplayMatrix(context.Background(), raw, ids, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("len=%d", len(res))
	}
}

// ---------- ReplayMatrix ----------

func TestEngine_ReplayMatrix_SingleBaselineVariant(t *testing.T) {
	// 单 baseline variant 时返回值应按 ids 顺序、每条 VariantName="baseline"。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	raw := RawRequest{Method: "GET", URL: srv.URL + "/", Headers: http.Header{}}
	ids := []credential.Identity{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	res, err := NewEngine(srv.Client()).ReplayMatrix(context.Background(), raw, ids, nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 3 {
		t.Fatalf("len=%d", len(res))
	}
	for i, want := range []string{"a", "b", "c"} {
		if res[i].IdentityName != want {
			t.Fatalf("res[%d].IdentityName=%q want %q", i, res[i].IdentityName, want)
		}
		if res[i].VariantName != BaselineVariantName {
			t.Fatalf("res[%d].VariantName=%q want baseline", i, res[i].VariantName)
		}
	}
}

func TestEngine_ReplayMatrix_CartesianOrder(t *testing.T) {
	// identity × variant 笛卡尔积，identity 外层、variant 内层：
	// out[0]=(a,baseline) out[1]=(a,vX) out[2]=(b,baseline) out[3]=(b,vX) ...
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	raw := RawRequest{Method: "GET", URL: srv.URL + "/", Headers: http.Header{}}
	ids := []credential.Identity{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	variants := []Variant{
		DefaultBaselineVariant(),
		{Name: "vX", Mutation: Mutation{Type: MutationPassthrough}},
	}

	res, err := NewEngine(srv.Client()).ReplayMatrix(context.Background(), raw, ids, variants, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 6 {
		t.Fatalf("len=%d want 6 (3 ids × 2 variants)", len(res))
	}
	if hits.Load() != 6 {
		t.Fatalf("hits=%d want 6", hits.Load())
	}
	expectations := []struct{ id, v string }{
		{"a", "baseline"}, {"a", "vX"},
		{"b", "baseline"}, {"b", "vX"},
		{"c", "baseline"}, {"c", "vX"},
	}
	for i, want := range expectations {
		if res[i].IdentityName != want.id || res[i].VariantName != want.v {
			t.Fatalf("res[%d]=(%q,%q) want (%q,%q)",
				i, res[i].IdentityName, res[i].VariantName, want.id, want.v)
		}
	}
}

func TestEngine_ReplayMatrix_UnsupportedMutation_PerCellError(t *testing.T) {
	// 不支持的 mutation 类型只让该 cell 失败（ErrorMessage 暴露），不影响其他 cell。
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	raw := RawRequest{Method: "GET", URL: srv.URL + "/", Headers: http.Header{}}
	ids := []credential.Identity{{Name: "a"}}
	variants := []Variant{
		DefaultBaselineVariant(),
		{Name: "bad", Mutation: Mutation{Type: "param_inject"}}, // Step 1 还没实现
	}

	res, err := NewEngine(srv.Client()).ReplayMatrix(context.Background(), raw, ids, variants, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(res) != 2 {
		t.Fatalf("len=%d", len(res))
	}
	if res[0].VariantName != "baseline" || res[0].StatusCode != 200 {
		t.Fatalf("baseline cell 应正常: %+v", res[0])
	}
	if res[1].VariantName != "bad" || res[1].ErrorMessage == "" {
		t.Fatalf("不支持 mutation 应返回 ErrorMessage，got %+v", res[1])
	}
}

// ---------- ApplyMutation: param_inject ----------

func TestApplyMutation_ParamInject_QueryReplace(t *testing.T) {
	raw := RawRequest{Method: "GET", URL: "http://x/api?id=7&keep=1", Headers: http.Header{}}
	m := Mutation{Type: MutationParamInject, Where: WhereQuery, Field: "id", Value: "1' OR '1'='1"}
	out, err := ApplyMutation(raw, m)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.URL, "id=1%27+OR+%271%27%3D%271") {
		t.Fatalf("query 应包含编码后的 payload，实际 %q", out.URL)
	}
	if !strings.Contains(out.URL, "keep=1") {
		t.Fatalf("其他 query 字段不应丢失: %q", out.URL)
	}
	// 入参不应被修改（深拷贝语义）
	if raw.URL != "http://x/api?id=7&keep=1" {
		t.Fatalf("input raw 被污染: %q", raw.URL)
	}
}

func TestApplyMutation_ParamInject_QueryAppend(t *testing.T) {
	raw := RawRequest{Method: "GET", URL: "http://x/api?id=7", Headers: http.Header{}}
	m := Mutation{Type: MutationParamInject, Where: WhereQuery, Field: "id", Value: "' AND 1=1--", Mode: ModeAppend}
	out, err := ApplyMutation(raw, m)
	if err != nil {
		t.Fatal(err)
	}
	// append 模式应保留原值并拼接 payload
	if !strings.Contains(out.URL, "id=7%27") {
		t.Fatalf("append 模式应在原值 7 后追加 payload，实际 %q", out.URL)
	}
}

func TestApplyMutation_ParamInject_BodyJSON(t *testing.T) {
	raw := RawRequest{
		Method:  "POST",
		URL:     "http://x/api",
		Headers: http.Header{"Content-Type": {"application/json"}},
		Body:    []byte(`{"username":"alice","keep":"y"}`),
	}
	m := Mutation{Type: MutationParamInject, Where: WhereBodyJSON, Field: "username", Value: "alice' OR '1'='1"}
	out, err := ApplyMutation(raw, m)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(out.Body, &parsed); err != nil {
		t.Fatalf("body 不再是合法 JSON: %v", err)
	}
	if parsed["username"] != "alice' OR '1'='1" {
		t.Fatalf("username 未替换: %v", parsed["username"])
	}
	if parsed["keep"] != "y" {
		t.Fatalf("其他字段不应丢失: %v", parsed["keep"])
	}
}

func TestApplyMutation_ParamInject_BodyForm(t *testing.T) {
	raw := RawRequest{
		Method:  "POST",
		URL:     "http://x/login",
		Headers: http.Header{"Content-Type": {"application/x-www-form-urlencoded"}},
		Body:    []byte("username=alice&password=x"),
	}
	m := Mutation{Type: MutationParamInject, Where: WhereBodyForm, Field: "username", Value: "admin'--"}
	out, err := ApplyMutation(raw, m)
	if err != nil {
		t.Fatal(err)
	}
	values, _ := url.ParseQuery(string(out.Body))
	if values.Get("username") != "admin'--" {
		t.Fatalf("form username 未替换: %v", values.Get("username"))
	}
	if values.Get("password") != "x" {
		t.Fatalf("其他字段不应丢失: %v", values.Get("password"))
	}
}

func TestApplyMutation_ParamInject_RejectsEmptyField(t *testing.T) {
	raw := RawRequest{Method: "GET", URL: "http://x/", Headers: http.Header{}}
	m := Mutation{Type: MutationParamInject, Where: WhereQuery, Value: "x"}
	if _, err := ApplyMutation(raw, m); err == nil {
		t.Fatal("空 field 应报错")
	}
}

func TestApplyMutation_ParamInject_UnsupportedWhere(t *testing.T) {
	raw := RawRequest{Method: "GET", URL: "http://x/", Headers: http.Header{}}
	m := Mutation{Type: MutationParamInject, Where: "header_inject", Field: "X-Test", Value: "v"}
	if _, err := ApplyMutation(raw, m); err == nil {
		t.Fatal("未支持的 where 应报错")
	}
}

func TestEngine_ReplayMatrix_EmptyVariants_DegradesToBaseline(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}))
	defer srv.Close()

	raw := RawRequest{Method: "GET", URL: srv.URL + "/", Headers: http.Header{}}
	ids := []credential.Identity{{Name: "x"}}

	// 传 nil variants 应自动用 baseline 兜底。
	res, _ := NewEngine(srv.Client()).ReplayMatrix(context.Background(), raw, ids, nil, 1)
	if len(res) != 1 || res[0].VariantName != BaselineVariantName {
		t.Fatalf("nil variants 应退化为 baseline，got %+v", res)
	}

	// 传空切片同样应退化。
	res2, _ := NewEngine(srv.Client()).ReplayMatrix(context.Background(), raw, ids, []Variant{}, 1)
	if len(res2) != 1 || res2[0].VariantName != BaselineVariantName {
		t.Fatalf("空 variants 应退化为 baseline，got %+v", res2)
	}
}
