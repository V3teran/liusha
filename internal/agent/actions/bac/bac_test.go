package bac

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/replay"
)

// 编译期接口断言：真实 *flow.Store 应能直接喂进 Factory.FlowReader。
var (
	_ credential.Provider = (credential.Provider)(nil)
	_ FlowReader          = (*flow.Store)(nil)
)

// fakeProvider 实现 credential.Provider，用于隔离 Redis 依赖。
type fakeProvider struct {
	identities []credential.Identity
	getErr     error
	gotHost    string
}

func (f *fakeProvider) BatchSave(_ context.Context, _ map[string][]credential.Identity, _ int) error {
	return nil
}

func (f *fakeProvider) GetIdentitiesByHost(_ context.Context, host string) ([]credential.Identity, error) {
	f.gotHost = host
	if f.getErr != nil {
		return nil, f.getErr
	}
	return f.identities, nil
}

func (f *fakeProvider) List(_ context.Context, _ string) (map[string][]credential.Identity, error) {
	return nil, nil
}

func (f *fakeProvider) Delete(_ context.Context, _ string) error { return nil }

// fakeFlowReader 实现 FlowReader：用 map 模拟 id → flow 的反查。
type fakeFlowReader struct {
	flows map[int64]flow.Flow
}

func (f *fakeFlowReader) GetByID(_ context.Context, id int64) (flow.Flow, error) {
	v, ok := f.flows[id]
	if !ok {
		return flow.Flow{}, errors.New("not found")
	}
	return v, nil
}

// ---------- FetchCredentials ----------

func TestFetchCredentials_PopulatesSessionAndOmitsRawValues(t *testing.T) {
	prov := &fakeProvider{identities: []credential.Identity{
		{Name: "admin", Role: "admin", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "session=admin_secret_value"},
		}},
		{Name: credential.AnonymousName},
	}}
	session := &Session{}
	a := &FetchCredentials{Provider: prov, Session: session}

	out, err := a.Execute(context.Background(), json.RawMessage(`{"host":"example.com"}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if prov.gotHost != "example.com" {
		t.Fatalf("host 未透传到 Provider: %q", prov.gotHost)
	}
	if len(session.Identities) != 2 {
		t.Fatalf("session.Identities 应有 2 个，got %d", len(session.Identities))
	}
	// 输出不应泄漏 raw value（防 token 浪费 + 防误打印密钥）。
	if strings.Contains(string(out.Output), "admin_secret_value") {
		t.Fatalf("Output 不应包含 raw credential value: %s", string(out.Output))
	}
	if !strings.Contains(string(out.Output), `"has_credentials"`) {
		t.Fatalf("Output 应含 has_credentials 字段: %s", string(out.Output))
	}
}

func TestFetchCredentials_RejectsEmptyHost(t *testing.T) {
	a := &FetchCredentials{Provider: &fakeProvider{}, Session: &Session{}}
	_, err := a.Execute(context.Background(), json.RawMessage(`{"host":""}`))
	if err == nil {
		t.Fatal("空 host 应报错")
	}
}

// ---------- ReplayMultiIdentity ----------

func TestReplayMultiIdentity_RequiresFetchCredentialsFirst(t *testing.T) {
	a := &ReplayMultiIdentity{
		Engine:  replay.NewEngine(http.DefaultClient),
		Flows:   &fakeFlowReader{flows: map[int64]flow.Flow{1: {ID: 1, Method: "GET", URL: "http://x/"}}},
		Session: &Session{},
	}
	_, err := a.Execute(context.Background(), json.RawMessage(`{"flow_id":1,"host":"x"}`))
	if err == nil {
		t.Fatal("Identities 为空时应报错（提示先调 fetch_credentials）")
	}
	if !strings.Contains(err.Error(), "fetch_credentials") {
		t.Fatalf("错误信息应提示先调 fetch_credentials: %v", err)
	}
}

func TestReplayMultiIdentity_PopulatesSessionWithBodyHint(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"alice","email":"alice@example.com"}`))
	}))
	defer srv.Close()

	session := &Session{
		Identities: []credential.Identity{
			{Name: "admin"},
			{Name: "user"},
			{Name: credential.AnonymousName},
		},
	}
	flows := &fakeFlowReader{flows: map[int64]flow.Flow{
		7: {
			ID: 7, Method: "GET", URL: srv.URL + "/api/profile",
			RequestHeaders: json.RawMessage(`{"Accept":"application/json"}`),
		},
	}}
	a := &ReplayMultiIdentity{
		Engine:  replay.NewEngine(srv.Client()),
		Flows:   flows,
		Session: session,
	}

	out, err := a.Execute(context.Background(), json.RawMessage(`{"flow_id":7,"host":"example.com","concurrency":3}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("应命中 3 次（3 身份），got %d", hits.Load())
	}
	if len(session.LastResponses) != 3 {
		t.Fatalf("session.LastResponses 应写 3 条，got %d", len(session.LastResponses))
	}
	if session.LastFlow.ID != 7 {
		t.Fatalf("session.LastFlow 未写入: %+v", session.LastFlow)
	}
	if !strings.Contains(string(out.Output), "body_hint") {
		t.Fatalf("Output 应含 body_hint: %s", string(out.Output))
	}

	var got struct {
		Count     int `json:"count"`
		Responses []struct {
			Identity   string `json:"identity"`
			StatusCode int    `json:"status_code"`
			BodyHint   string `json:"body_hint"`
		} `json:"responses"`
	}
	if err := json.Unmarshal(out.Output, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Count != 3 {
		t.Fatalf("count 应为 3，got %d", got.Count)
	}
}

func TestReplayMultiIdentity_BodyHintTruncatedAt400(t *testing.T) {
	huge := strings.Repeat("X", 2000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(huge))
	}))
	defer srv.Close()

	session := &Session{Identities: []credential.Identity{{Name: "a"}}}
	flows := &fakeFlowReader{flows: map[int64]flow.Flow{
		1: {ID: 1, Method: "GET", URL: srv.URL + "/x"},
	}}
	a := &ReplayMultiIdentity{
		Engine:  replay.NewEngine(srv.Client()),
		Flows:   flows,
		Session: session,
	}
	out, err := a.Execute(context.Background(), json.RawMessage(`{"flow_id":1,"host":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Responses []struct {
			BodyHint string `json:"body_hint"`
		} `json:"responses"`
	}
	_ = json.Unmarshal(out.Output, &got)
	if len(got.Responses) != 1 {
		t.Fatalf("got=%d", len(got.Responses))
	}
	if l := len(got.Responses[0].BodyHint); l == 0 || l > 400 {
		t.Fatalf("body_hint 应被截到 ≤400，got %d", l)
	}
}

// ---------- HeuristicCheck ----------

func TestHeuristicCheck_RequiresPriorReplay(t *testing.T) {
	a := &HeuristicCheck{Session: &Session{}}
	_, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("LastResponses 为空时应报错")
	}
}

func TestHeuristicCheck_AllDenied_HitsSkip(t *testing.T) {
	session := &Session{LastResponses: []replay.Response{
		{IdentityName: "a", StatusCode: 403},
		{IdentityName: "b", StatusCode: 401},
	}}
	a := &HeuristicCheck{Session: session}
	out, err := a.Execute(context.Background(), json.RawMessage(`{"rules":["all_denied"]}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Skip    bool   `json:"skip"`
		HitRule string `json:"hit_rule"`
		Reason  string `json:"reason"`
	}
	_ = json.Unmarshal(out.Output, &got)
	if !got.Skip {
		t.Fatalf("应 skip=true: %s", string(out.Output))
	}
	if got.HitRule != "all_denied" {
		t.Fatalf("hit_rule 应为 all_denied: %q", got.HitRule)
	}
}

func TestHeuristicCheck_NoHitWithMixedStatus(t *testing.T) {
	session := &Session{LastResponses: []replay.Response{
		{IdentityName: "a", StatusCode: 200, Body: []byte(`{"data":1}`)},
		{IdentityName: "b", StatusCode: 403, Body: []byte(`forbidden`)},
	}}
	a := &HeuristicCheck{Session: session}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out.Output), `"skip":false`) {
		t.Fatalf("混合状态码不应 skip: %s", string(out.Output))
	}
}

// ---------- ComputeSimilarity ----------

func TestComputeSimilarity_RequiresPriorReplay(t *testing.T) {
	a := &ComputeSimilarity{Session: &Session{}}
	_, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("LastResponses 为空时应报错")
	}
}

func TestComputeSimilarity_AllBelowThreshold(t *testing.T) {
	session := &Session{LastResponses: []replay.Response{
		{IdentityName: "admin", StatusCode: 200, Body: []byte("alice profile data")},
		{IdentityName: "user", StatusCode: 200, Body: []byte("bob profile content")},
		{IdentityName: "anonymous", StatusCode: 401, Body: []byte("please login first")},
	}}
	a := &ComputeSimilarity{Session: session}
	out, err := a.Execute(context.Background(), json.RawMessage(`{"algorithm":"structural","threshold":0.9}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Matrix            [][]float64 `json:"matrix"`
		AllBelowThreshold bool        `json:"all_below_threshold"`
		MaxPair           struct {
			A     string  `json:"a"`
			B     string  `json:"b"`
			Score float64 `json:"score"`
		} `json:"max_pair"`
	}
	if err := json.Unmarshal(out.Output, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Matrix) != 3 || len(got.Matrix[0]) != 3 {
		t.Fatalf("matrix 应为 3x3: %v", got.Matrix)
	}
	if !got.AllBelowThreshold {
		t.Fatalf("阈值 0.9 下应 all_below_threshold=true: %s", string(out.Output))
	}
	if got.MaxPair.A == "" || got.MaxPair.B == "" {
		t.Fatalf("max_pair 应填充: %+v", got.MaxPair)
	}
}

func TestComputeSimilarity_HighSimilarityFlagsBreak(t *testing.T) {
	session := &Session{LastResponses: []replay.Response{
		{IdentityName: "admin", StatusCode: 200, Body: []byte("private order data 12345")},
		{IdentityName: "user", StatusCode: 200, Body: []byte("private order data 12345")},
		{IdentityName: "anonymous", StatusCode: 200, Body: []byte("private order data 12345")},
	}}
	a := &ComputeSimilarity{Session: session}
	out, _ := a.Execute(context.Background(), json.RawMessage(`{"threshold":0.5}`))
	var got struct {
		AllBelowThreshold bool `json:"all_below_threshold"`
	}
	_ = json.Unmarshal(out.Output, &got)
	if got.AllBelowThreshold {
		t.Fatalf("三条相同 body 在阈值 0.5 下不应 all_below_threshold=true: %s", string(out.Output))
	}
}

// ---------- Factory ----------

func TestFactory_CreateActions_SharesSession(t *testing.T) {
	prov := &fakeProvider{identities: []credential.Identity{{Name: "x"}}}
	flows := &fakeFlowReader{}
	eng := replay.NewEngine(http.DefaultClient)

	f := NewFactory(prov, flows, eng)
	acts := f.CreateActions("eng-1")
	if len(acts) != 4 {
		t.Fatalf("应返回 4 个 action，got %d", len(acts))
	}

	names := make(map[string]tool.Action, len(acts))
	for _, a := range acts {
		names[a.Name()] = a
	}
	for _, want := range []string{"fetch_credentials", "replay_multi_identity", "heuristic_check", "compute_similarity"} {
		if _, ok := names[want]; !ok {
			t.Fatalf("缺少 action: %s", want)
		}
	}

	if _, err := names["fetch_credentials"].Execute(
		context.Background(),
		json.RawMessage(`{"host":"shared.com"}`),
	); err != nil {
		t.Fatalf("fetch err=%v", err)
	}
	fc := names["fetch_credentials"].(*FetchCredentials)
	rm := names["replay_multi_identity"].(*ReplayMultiIdentity)
	hc := names["heuristic_check"].(*HeuristicCheck)
	cs := names["compute_similarity"].(*ComputeSimilarity)
	if fc.Session != rm.Session || rm.Session != hc.Session || hc.Session != cs.Session {
		t.Fatal("4 个 action 应共享同一个 *Session")
	}
	if len(fc.Session.Identities) != 1 {
		t.Fatalf("fetch 写入应可被其他 action 读到：Identities=%d", len(fc.Session.Identities))
	}
}

func TestFactory_Register_AllNames(t *testing.T) {
	prov := &fakeProvider{}
	flows := &fakeFlowReader{}
	eng := replay.NewEngine(http.DefaultClient)
	f := NewFactory(prov, flows, eng)

	reg := tool.NewRegistry()
	if err := f.Register(reg, "eng-2"); err != nil {
		t.Fatalf("Register err=%v", err)
	}
	for _, want := range []string{"fetch_credentials", "replay_multi_identity", "heuristic_check", "compute_similarity"} {
		if !reg.Has(want) {
			t.Fatalf("Registry 应有 %s", want)
		}
	}
}
