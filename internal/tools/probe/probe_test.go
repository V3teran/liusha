package probe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/replay"
	"github.com/V3teran/liusha/internal/toolruntime"
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

func TestFetchCredentials_PopulatesStateAndOmitsRawValues(t *testing.T) {
	prov := &fakeProvider{identities: []credential.Identity{
		{Name: "admin", Role: "admin", Credentials: []credential.Credential{
			{Type: credential.TypeHeaders, Key: "Cookie", Value: "session=admin_secret_value"},
		}},
		{Name: credential.AnonymousName},
	}}
	state := &ProbeState{}
	a := &FetchCredentials{Provider: prov, State: state}

	out, err := a.Execute(context.Background(), json.RawMessage(`{"host":"example.com"}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if prov.gotHost != "example.com" {
		t.Fatalf("host 未透传到 Provider: %q", prov.gotHost)
	}
	if len(state.Identities) != 2 {
		t.Fatalf("state.Identities 应有 2 个，got %d", len(state.Identities))
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
	a := &FetchCredentials{Provider: &fakeProvider{}, State: &ProbeState{}}
	_, err := a.Execute(context.Background(), json.RawMessage(`{"host":""}`))
	if err == nil {
		t.Fatal("空 host 应报错")
	}
}

// ---------- ReplayMatrix ----------

func TestReplayMatrix_RequiresFetchCredentialsFirst(t *testing.T) {
	a := &ReplayMatrix{
		Engine: replay.NewEngine(http.DefaultClient, 0),
		Flows:  &fakeFlowReader{flows: map[int64]flow.Flow{1: {ID: 1, Method: "GET", URL: "http://x/"}}},
		State:  &ProbeState{},
	}
	_, err := a.Execute(context.Background(), json.RawMessage(`{"flow_id":1,"host":"x"}`))
	if err == nil {
		t.Fatal("Identities 为空时应报错（提示先调 fetch_credentials）")
	}
	if !strings.Contains(err.Error(), "fetch_credentials") {
		t.Fatalf("错误信息应提示先调 fetch_credentials: %v", err)
	}
}

func TestReplayMatrix_PopulatesStateWithBodyHint(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"name":"alice","email":"alice@example.com"}`))
	}))
	defer srv.Close()

	state := &ProbeState{
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
	a := &ReplayMatrix{
		Engine: replay.NewEngine(srv.Client(), 0),
		Flows:  flows,
		State:  state,
	}

	out, err := a.Execute(context.Background(), json.RawMessage(`{"flow_id":7,"host":"example.com","concurrency":3}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if hits.Load() != 3 {
		t.Fatalf("应命中 3 次（3 身份），got %d", hits.Load())
	}
	if len(state.LastResponses) != 3 {
		t.Fatalf("state.LastResponses 应写 3 条，got %d", len(state.LastResponses))
	}
	if state.LastFlow.ID != 7 {
		t.Fatalf("state.LastFlow 未写入: %+v", state.LastFlow)
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

func TestReplayMatrix_BodyHintTruncatedAt8192(t *testing.T) {
	huge := strings.Repeat("X", 10000)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(huge))
	}))
	defer srv.Close()

	state := &ProbeState{Identities: []credential.Identity{{Name: "a"}}}
	flows := &fakeFlowReader{flows: map[int64]flow.Flow{
		1: {ID: 1, Method: "GET", URL: srv.URL + "/x"},
	}}
	a := &ReplayMatrix{
		Engine: replay.NewEngine(srv.Client(), 0),
		Flows:  flows,
		State:  state,
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
	if l := len(got.Responses[0].BodyHint); l == 0 || l > 8192 {
		t.Fatalf("body_hint 应被截到 ≤8192，got %d", l)
	}
}

// ---------- HeuristicCheck ----------

func TestHeuristicCheck_RequiresPriorReplay(t *testing.T) {
	a := &HeuristicCheck{State: &ProbeState{}}
	_, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("LastResponses 为空时应报错")
	}
}

func TestHeuristicCheck_AllDenied_HitsSkip(t *testing.T) {
	state := &ProbeState{LastResponses: []replay.Response{
		{IdentityName: "a", StatusCode: 403},
		{IdentityName: "b", StatusCode: 401},
	}}
	a := &HeuristicCheck{State: state}
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
	state := &ProbeState{LastResponses: []replay.Response{
		{IdentityName: "a", StatusCode: 200, Body: []byte(`{"data":1}`)},
		{IdentityName: "b", StatusCode: 403, Body: []byte(`forbidden`)},
	}}
	a := &HeuristicCheck{State: state}
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
	a := &ComputeSimilarity{State: &ProbeState{}}
	_, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err == nil {
		t.Fatal("LastResponses 为空时应报错")
	}
}

// similarityResult 是 probe_test.go 内部解码 ComputeSimilarity 输出用的视图（agentic：无 verdict）。
type similarityResult struct {
	Algorithm       string  `json:"algorithm"`
	MinThreshold    float64 `json:"min_threshold"`
	HighThreshold   float64 `json:"high_threshold"`
	SuspiciousPairs []struct {
		A           string  `json:"a"`
		B           string  `json:"b"`
		Score       float64 `json:"score"`
		LengthRatio float64 `json:"length_ratio"`
	} `json:"suspicious_pairs"`
	Summary struct {
		TotalPairs int     `json:"total_pairs"`
		MaxScore   float64 `json:"max_score"`
		MinScore   float64 `json:"min_score"`
		AboveHigh  int     `json:"above_high_threshold"`
		AboveMin   int     `json:"above_min_threshold"`
	} `json:"summary"`
}

func TestComputeSimilarity_AllBelowThreshold(t *testing.T) {
	// 三个内容毫不相关的 body：所有 pair 应低于 min_threshold=0.6
	// LLM 看 above_min=0 / max_score < 0.6 自判"无身份相似" → done(no_pattern_match)。
	state := &ProbeState{LastResponses: []replay.Response{
		{IdentityName: "admin", StatusCode: 200, Body: []byte("alice profile data")},
		{IdentityName: "user", StatusCode: 200, Body: []byte("bob profile content")},
		{IdentityName: "anonymous", StatusCode: 401, Body: []byte("please login first")},
	}}
	a := &ComputeSimilarity{State: state}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got similarityResult
	if err := json.Unmarshal(out.Output, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Summary.AboveMin != 0 {
		t.Fatalf("无相似对时 above_min 应=0，实际=%d", got.Summary.AboveMin)
	}
	if len(got.SuspiciousPairs) != 0 {
		t.Fatalf("无相似对时不应有 suspicious_pairs，实际=%d", len(got.SuspiciousPairs))
	}
	if got.Summary.TotalPairs != 3 {
		t.Fatalf("3 身份应有 3 pair，实际=%d", got.Summary.TotalPairs)
	}
}

func TestComputeSimilarity_HighSimilarity(t *testing.T) {
	// 三身份 body 完全相同：max=1.0 ≥ high_threshold=0.9，above_high=3
	// LLM 看到所有 pair score=1.0 自判"疑似越权"（或公开接口，由 SKILL 决策）。
	state := &ProbeState{LastResponses: []replay.Response{
		{IdentityName: "admin", StatusCode: 200, Body: []byte("private order data 12345")},
		{IdentityName: "user", StatusCode: 200, Body: []byte("private order data 12345")},
		{IdentityName: "anonymous", StatusCode: 200, Body: []byte("private order data 12345")},
	}}
	a := &ComputeSimilarity{State: state}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got similarityResult
	if err := json.Unmarshal(out.Output, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.SuspiciousPairs) != 3 {
		t.Fatalf("3 身份完全相同应有 3 个 pair 进 suspicious_pairs，实际=%d", len(got.SuspiciousPairs))
	}
	if got.Summary.AboveHigh != 3 {
		t.Fatalf("Summary.AboveHigh 应=3，实际=%d", got.Summary.AboveHigh)
	}
	if got.Summary.MaxScore < 0.99 {
		t.Fatalf("max_score 应=1.0（完全相同 body），实际=%v", got.Summary.MaxScore)
	}
}

func TestComputeSimilarity_AmbiguousBetweenThresholds(t *testing.T) {
	// 两个 body 共享部分 token（jaccard ≈ 0.7-0.8），落在 [0.6, 0.9) 区间。
	state := &ProbeState{LastResponses: []replay.Response{
		{IdentityName: "admin", StatusCode: 200, Body: []byte("alpha beta gamma delta epsilon zeta")},
		{IdentityName: "user", StatusCode: 200, Body: []byte("alpha beta gamma delta epsilon eta")},
	}}
	a := &ComputeSimilarity{State: state}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got similarityResult
	if err := json.Unmarshal(out.Output, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Summary.AboveMin != 1 || got.Summary.AboveHigh != 0 {
		t.Fatalf("应 above_min=1 / above_high=0（[min,high) 模糊区），实际 above_min=%d above_high=%d max=%v",
			got.Summary.AboveMin, got.Summary.AboveHigh, got.Summary.MaxScore)
	}
	if len(got.SuspiciousPairs) != 1 {
		t.Fatalf("应 1 个 suspicious_pair，实际=%d", len(got.SuspiciousPairs))
	}
}

func TestComputeSimilarity_LengthRatioShortCircuit(t *testing.T) {
	// 一个 5 字节、一个 500 字节，length_ratio=0.01 < gate=0.3 → score 强制为 0，
	// 即使 token 有重叠也判为 all_below_threshold。
	short := []byte("hello")
	long := make([]byte, 0, 500)
	for i := 0; i < 50; i++ {
		long = append(long, []byte("hello world ")...)
	}
	state := &ProbeState{LastResponses: []replay.Response{
		{IdentityName: "admin", StatusCode: 200, Body: short},
		{IdentityName: "user", StatusCode: 500, Body: long},
	}}
	a := &ComputeSimilarity{State: state}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got similarityResult
	if err := json.Unmarshal(out.Output, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Summary.AboveMin != 0 {
		t.Fatalf("length-gate 命中时 above_min 应=0，实际=%d max=%v",
			got.Summary.AboveMin, got.Summary.MaxScore)
	}
	if got.Summary.MaxScore != 0 {
		t.Fatalf("length-gate 命中时 score 应为 0，实际=%v", got.Summary.MaxScore)
	}
}

// ---------- ComputeSimilarity (baseline 模式：含 _original_ 锚点) ----------

// baselineSimilarityResult 是 baseline 模式下 ComputeSimilarity 输出的解码视图（agentic：无 verdict）。
type baselineSimilarityResult struct {
	Algorithm     string   `json:"algorithm"`
	Mode          string   `json:"mode"`
	Baseline      string   `json:"baseline"`
	Identities    []string `json:"identities"`
	BaselinePairs []struct {
		A           string  `json:"a"`
		B           string  `json:"b"`
		Score       float64 `json:"score"`
		LengthRatio float64 `json:"length_ratio"`
	} `json:"baseline_pairs"`
	Summary struct {
		TotalPairs int     `json:"total_pairs"`
		MaxScore   float64 `json:"max_score"`
		MinScore   float64 `json:"min_score"`
		AboveHigh  int     `json:"above_high_threshold"`
		AboveMin   int     `json:"above_min_threshold"`
	} `json:"summary"`
}

func TestComputeSimilarity_Baseline_AllDissimilar_ShortCircuit(t *testing.T) {
	// 抓包原始：合法用户看到的私有订单数据；3 个 replay 全部 401 错误页 → 全不像 baseline。
	state := &ProbeState{
		LastFlow: flow.Flow{
			ID:           42,
			StatusCode:   200,
			ResponseBody: []byte(`{"order_id":7,"buyer":"alice","amount":100}`),
		},
		LastResponses: []replay.Response{
			{IdentityName: "admin", StatusCode: 401, Body: []byte(`{"error":"unauthorized"}`)},
			{IdentityName: "user", StatusCode: 401, Body: []byte(`{"error":"forbidden"}`)},
			{IdentityName: credential.AnonymousName, StatusCode: 401, Body: []byte(`{"error":"login required"}`)},
		},
	}
	a := &ComputeSimilarity{State: state}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got baselineSimilarityResult
	if err := json.Unmarshal(out.Output, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got.Mode != "baseline" {
		t.Fatalf("应 mode=baseline，实际=%q", got.Mode)
	}
	if got.Summary.AboveMin != 0 {
		t.Fatalf("所有身份均不像原始时 above_min 应=0，实际=%d", got.Summary.AboveMin)
	}
	if got.Baseline != replay.OriginalIdentityName {
		t.Fatalf("baseline 应=%q，实际=%q", replay.OriginalIdentityName, got.Baseline)
	}
	for _, name := range got.Identities {
		if name == replay.OriginalIdentityName {
			t.Fatalf("identities 不应含 _original_，实际=%v", got.Identities)
		}
	}
	if len(got.BaselinePairs) != 3 {
		t.Fatalf("应有 3 个 baseline_pair（每 replay 一个），实际=%d", len(got.BaselinePairs))
	}
	for _, p := range got.BaselinePairs {
		if p.A != replay.OriginalIdentityName {
			t.Fatalf("baseline_pair.A 应固定=%q，实际=%q", replay.OriginalIdentityName, p.A)
		}
	}
}

func TestComputeSimilarity_Baseline_SomeMatch_StrongSignal(t *testing.T) {
	// admin 看到了原 buyer 的私有数据（horizontal 越权信号）；user/anon 被拒。
	body := []byte(`{"order_id":7,"buyer":"alice","amount":100}`)
	state := &ProbeState{
		LastFlow: flow.Flow{ID: 1, StatusCode: 200, ResponseBody: body},
		LastResponses: []replay.Response{
			{IdentityName: "admin", StatusCode: 200, Body: body},
			{IdentityName: "user", StatusCode: 401, Body: []byte(`{"error":"forbidden"}`)},
			{IdentityName: credential.AnonymousName, StatusCode: 401, Body: []byte(`{"error":"login"}`)},
		},
	}
	a := &ComputeSimilarity{State: state}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got baselineSimilarityResult
	_ = json.Unmarshal(out.Output, &got)
	if got.Summary.AboveHigh != 1 {
		t.Fatalf("应 above_high=1（仅 admin 高度匹配），实际=%d payload=%s",
			got.Summary.AboveHigh, string(out.Output))
	}
	if got.Summary.AboveHigh == got.Summary.TotalPairs {
		t.Fatalf("不应 above_high == total（不是公开接口），above_high=%d total=%d",
			got.Summary.AboveHigh, got.Summary.TotalPairs)
	}
}

func TestComputeSimilarity_Baseline_AllMatch_PublicEndpoint(t *testing.T) {
	// 公开接口：所有身份都看到与原始相同的数据 → all_match_baseline（提示公开接口可能）。
	body := []byte(`{"banner":"welcome","version":"v1.2"}`)
	state := &ProbeState{
		LastFlow: flow.Flow{ID: 1, StatusCode: 200, ResponseBody: body},
		LastResponses: []replay.Response{
			{IdentityName: "admin", StatusCode: 200, Body: body},
			{IdentityName: "user", StatusCode: 200, Body: body},
			{IdentityName: credential.AnonymousName, StatusCode: 200, Body: body},
		},
	}
	a := &ComputeSimilarity{State: state}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got baselineSimilarityResult
	_ = json.Unmarshal(out.Output, &got)
	if got.Summary.AboveHigh != 3 || got.Summary.TotalPairs != 3 {
		t.Fatalf("应 above_high=3 == total=3（公开接口），实际 above_high=%d total=%d",
			got.Summary.AboveHigh, got.Summary.TotalPairs)
	}
}

func TestComputeSimilarity_Baseline_Ambiguous_NoneAboveHigh(t *testing.T) {
	// 中间区：admin 中度相似（同 schema 但部分值不同），其他被拒。
	state := &ProbeState{
		LastFlow: flow.Flow{
			ID: 1, StatusCode: 200,
			ResponseBody: []byte(`{"order_id":7,"buyer":"alice","amount":100}`),
		},
		LastResponses: []replay.Response{
			// 同 schema，部分值不同 → JSON-aware 算分应在 [0.6, 0.9) 区间。
			{IdentityName: "admin", StatusCode: 200, Body: []byte(`{"order_id":99,"buyer":"alice","amount":100}`)},
			{IdentityName: "user", StatusCode: 401, Body: []byte(`{"error":"forbidden"}`)},
		},
	}
	a := &ComputeSimilarity{State: state}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got baselineSimilarityResult
	_ = json.Unmarshal(out.Output, &got)
	if got.Summary.AboveMin == 0 || got.Summary.AboveHigh > 0 {
		t.Fatalf("应 above_min>=1 / above_high=0（[min,high) 模糊区），实际 above_min=%d above_high=%d max=%v",
			got.Summary.AboveMin, got.Summary.AboveHigh, got.Summary.MaxScore)
	}
}

func TestComputeSimilarity_Baseline_FallsBackWhenNoOriginal(t *testing.T) {
	// 没有 LastFlow → AllResponses 不注入 baseline → 走 inter_pairs fallback。
	state := &ProbeState{LastResponses: []replay.Response{
		{IdentityName: "admin", StatusCode: 200, Body: []byte("alpha beta")},
		{IdentityName: "user", StatusCode: 200, Body: []byte("gamma delta")},
	}}
	a := &ComputeSimilarity{State: state}
	out, err := a.Execute(context.Background(), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		Mode    string `json:"mode"`
		Summary struct {
			AboveMin int `json:"above_min_threshold"`
		} `json:"summary"`
	}
	_ = json.Unmarshal(out.Output, &got)
	if got.Mode != "inter_pairs" {
		t.Fatalf("无 baseline 应回到 inter_pairs 模式，实际=%q", got.Mode)
	}
	if got.Summary.AboveMin != 0 {
		t.Fatalf("两个 body 完全不同应 above_min=0，实际=%d", got.Summary.AboveMin)
	}
}

func TestComputeSimilarity_ThresholdValidation(t *testing.T) {
	// high_threshold < min_threshold 应直接报错。
	state := &ProbeState{LastResponses: []replay.Response{
		{IdentityName: "a", Body: []byte("x")},
		{IdentityName: "b", Body: []byte("y")},
	}}
	a := &ComputeSimilarity{State: state}
	_, err := a.Execute(context.Background(), json.RawMessage(`{"min_threshold":0.9,"high_threshold":0.5}`))
	if err == nil {
		t.Fatal("high_threshold < min_threshold 应报错")
	}
}

// ---------- ProbeState.AllResponses ----------

func TestProbeState_AllResponses_NoFlow_ReturnsLastResponsesOnly(t *testing.T) {
	state := &ProbeState{LastResponses: []replay.Response{
		{IdentityName: "admin", StatusCode: 200, Body: []byte(`{"x":1}`)},
		{IdentityName: "user", StatusCode: 200, Body: []byte(`{"x":2}`)},
	}}
	got := state.AllResponses()
	if len(got) != 2 {
		t.Fatalf("无 LastFlow 应返回 2 条（原 LastResponses），got %d", len(got))
	}
	for _, r := range got {
		if r.IdentityName == OriginalIdentityName {
			t.Fatalf("无 LastFlow 不应注入 _original_，got %+v", r)
		}
	}
}

func TestProbeState_AllResponses_EmptyResponseBody_ReturnsLastResponsesOnly(t *testing.T) {
	state := &ProbeState{
		LastFlow:      flow.Flow{ID: 7, StatusCode: 200, ResponseBody: nil},
		LastResponses: []replay.Response{{IdentityName: "admin", Body: []byte("x")}},
	}
	got := state.AllResponses()
	if len(got) != 1 {
		t.Fatalf("空 ResponseBody 不应注入 _original_，got %d", len(got))
	}
}

func TestProbeState_AllResponses_PrependsOriginal(t *testing.T) {
	state := &ProbeState{
		LastFlow: flow.Flow{
			ID:           42,
			StatusCode:   200,
			ResponseBody: []byte(`{"order_id":"O1003","owner":"alice"}`),
		},
		LastResponses: []replay.Response{
			{IdentityName: "admin", StatusCode: 200, Body: []byte(`{"order_id":"O1003"}`)},
			{IdentityName: credential.AnonymousName, StatusCode: 401, Body: []byte(`{"err":"unauthorized"}`)},
		},
	}
	got := state.AllResponses()
	if len(got) != 3 {
		t.Fatalf("应在头部注入 _original_，got len=%d", len(got))
	}
	if got[0].IdentityName != OriginalIdentityName {
		t.Fatalf("头部应是 _original_，got %q", got[0].IdentityName)
	}
	if got[0].StatusCode != 200 {
		t.Fatalf("baseline 应继承 LastFlow.StatusCode=200，got %d", got[0].StatusCode)
	}
	if string(got[0].Body) != `{"order_id":"O1003","owner":"alice"}` {
		t.Fatalf("baseline.Body 应来自 LastFlow.ResponseBody，got %q", string(got[0].Body))
	}
	if got[1].IdentityName != "admin" || got[2].IdentityName != credential.AnonymousName {
		t.Fatalf("原 LastResponses 顺序应保留，got [%s, %s]", got[1].IdentityName, got[2].IdentityName)
	}
}

func TestProbeState_AllResponses_ReturnsFreshSlice(t *testing.T) {
	// 防别名 bug：返回的切片即使 append 也不应修改 state.LastResponses。
	state := &ProbeState{
		LastFlow:      flow.Flow{ID: 1, StatusCode: 200, ResponseBody: []byte("x")},
		LastResponses: []replay.Response{{IdentityName: "a"}},
	}
	got := state.AllResponses()
	got = append(got, replay.Response{IdentityName: "intruder"})
	if len(state.LastResponses) != 1 {
		t.Fatalf("外部 append 不应污染 state.LastResponses，got len=%d", len(state.LastResponses))
	}
}

// ---------- Factory ----------

func TestFactory_CreateActions_SharesState(t *testing.T) {
	prov := &fakeProvider{identities: []credential.Identity{{Name: "x"}}}
	flows := &fakeFlowReader{}
	eng := replay.NewEngine(http.DefaultClient, 0)

	f := NewFactory(prov, flows, eng)
	acts := f.CreateActions("eng-1", nil)
	if len(acts) != 4 {
		t.Fatalf("应返回 4 个 action，got %d", len(acts))
	}

	names := make(map[string]toolfx.Action, len(acts))
	for _, a := range acts {
		names[a.Name()] = a
	}
	for _, want := range []string{"fetch_credentials", "run_replay", "check_heuristics", "compute_similarity"} {
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
	rm := names["run_replay"].(*ReplayMatrix)
	hc := names["check_heuristics"].(*HeuristicCheck)
	cs := names["compute_similarity"].(*ComputeSimilarity)
	if fc.State != rm.State || rm.State != hc.State || hc.State != cs.State {
		t.Fatal("4 个 action 应共享同一个 *ProbeState")
	}
	if len(fc.State.Identities) != 1 {
		t.Fatalf("fetch 写入应可被其他 action 读到：Identities=%d", len(fc.State.Identities))
	}
}

func TestFactory_Register_AllNames(t *testing.T) {
	prov := &fakeProvider{}
	flows := &fakeFlowReader{}
	eng := replay.NewEngine(http.DefaultClient, 0)
	f := NewFactory(prov, flows, eng)

	reg := toolfx.NewRegistry()
	if err := f.Register(reg, "eng-2", nil); err != nil {
		t.Fatalf("Register err=%v", err)
	}
	for _, want := range []string{"fetch_credentials", "run_replay", "check_heuristics", "compute_similarity"} {
		if !reg.Has(want) {
			t.Fatalf("Registry 应有 %s", want)
		}
	}
}
