package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/V3teran/liusha/internal/llminvocation"

	"github.com/jackc/pgx/v5"
)

// fakeInvocations 是 InvocationsAPI 的内存实现，用于路由级单测。
type fakeInvocations struct {
	rows      []llminvocation.Invocation
	byID      map[int64]llminvocation.Invocation
	agg       llminvocation.Aggregate
	err       error
	gotFilter llminvocation.ListFilter // 列表收到的筛选（断言 query 解析）
	gotAggF   llminvocation.ListFilter // 统计收到的筛选（断言与列表同源）
	gotFacetF llminvocation.ListFilter // 选项收到的筛选
	facets    llminvocation.Facets
}

func (f *fakeInvocations) Facets(_ context.Context, ftr llminvocation.ListFilter) (llminvocation.Facets, error) {
	f.gotFacetF = ftr
	return f.facets, f.err
}

func (f *fakeInvocations) Flush(_ context.Context) error { return nil }

func (f *fakeInvocations) ListAll(_ context.Context, ftr llminvocation.ListFilter) ([]llminvocation.Invocation, error) {
	f.gotFilter = ftr
	return f.rows, f.err
}

func (f *fakeInvocations) CountAll(_ context.Context, _ llminvocation.ListFilter) (int, error) {
	return len(f.rows), f.err
}

func (f *fakeInvocations) AggregateAll(_ context.Context, ftr llminvocation.ListFilter) (llminvocation.Aggregate, error) {
	f.gotAggF = ftr
	return f.agg, f.err
}

func (f *fakeInvocations) GetDetailByID(_ context.Context, id int64) (llminvocation.Invocation, error) {
	v, ok := f.byID[id]
	if !ok {
		return llminvocation.Invocation{}, pgx.ErrNoRows
	}
	return v, nil
}

func TestLLMInvocationsHandler_Detail(t *testing.T) {
	t.Run("200 返回完整行含 messages/result", func(t *testing.T) {
		taskID := "t1"
		fake := &fakeInvocations{byID: map[int64]llminvocation.Invocation{
			5: {ID: 5, TaskID: &taskID, Messages: []byte(`[{"role":"user"}]`), Result: []byte(`{"x":1}`)},
		}}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/api/llm/invocations/detail/5", nil)
		req.AddCookie(testSessionCookie())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != 200 {
			t.Fatalf("状态码=%d，期望 200", resp.StatusCode)
		}
		var raw map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		if raw["messages"] == nil || raw["result"] == nil {
			t.Errorf("详情响应应含 messages/result: %+v", raw)
		}
	})

}

func TestLLMInvocationsHandler_SearchStat(t *testing.T) {
	t.Run("200 返回筛选结果集的合计（与翻页无关）", func(t *testing.T) {
		fake := &fakeInvocations{agg: llminvocation.Aggregate{Calls: 197, InTokens: 509245, OutTokens: 40121, CachedTokens: 148800, LatencyMs: 12345678}}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/api/llm/invocations/search/stat?only_err=1", nil)
		req.AddCookie(testSessionCookie())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != 200 {
			t.Fatalf("状态码=%d，期望 200", resp.StatusCode)
		}
		var raw map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"calls", "in_tokens", "out_tokens", "cached_tokens", "latency_ms"} {
			if raw[key] == nil {
				t.Errorf("统计响应缺字段 %s: %+v", key, raw)
			}
		}
		if raw["calls"].(float64) != 197 {
			t.Errorf("calls=%v，期望 197", raw["calls"])
		}
	})
}

// 角色 / 模型选项走服务端：与列表同一套筛选参数透传。
func TestLLMInvocationsHandler_Facets(t *testing.T) {
	fake := &fakeInvocations{facets: llminvocation.Facets{Roles: []string{"executor", "planner"}, Models: []string{"glm"}}}
	srv := newTestServer(t, Deps{Invocations: fake})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/llm/invocations/search/facets?model=glm&only_err=1", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	roles, _ := body["roles"].([]any)
	if len(roles) != 2 || roles[0] != "executor" {
		t.Fatalf("roles=%v", body["roles"])
	}
	if fake.gotFacetF.Model != "glm" || !fake.gotFacetF.OnlyErr {
		t.Fatalf("筛选未透传: %+v", fake.gotFacetF)
	}
}

// 时间范围写错时 400，而不是悄悄不筛；列表、统计、分面同一口径。
func TestLLMInvocationsHandler_InvalidTimeRejected(t *testing.T) {
	srv := newTestServer(t, Deps{Invocations: &fakeInvocations{}})
	defer srv.Close()
	for _, path := range []string{"/api/llm/invocations/search", "/api/llm/invocations/search/stat", "/api/llm/invocations/search/facets"} {
		for _, q := range []string{"start=bad", "end=2026-02-30T00:00:00Z"} {
			resp := doReq(t, "GET", srv.URL+path+"?"+q, "")
			_ = resp.Body.Close()
			if resp.StatusCode != 400 {
				t.Errorf("%s?%s 应 400，得 %d", path, q, resp.StatusCode)
			}
		}
	}
}
