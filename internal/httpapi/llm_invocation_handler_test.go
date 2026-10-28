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
}

func (f *fakeInvocations) Flush(_ context.Context) error { return nil }

func (f *fakeInvocations) ListAll(_ context.Context, _ llminvocation.ListFilter) ([]llminvocation.Invocation, error) {
	return f.rows, f.err
}

func (f *fakeInvocations) CountAll(_ context.Context, _ llminvocation.ListFilter) (int, error) {
	return len(f.rows), f.err
}

func (f *fakeInvocations) AggregateAll(_ context.Context, _ llminvocation.ListFilter) (llminvocation.Aggregate, error) {
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
			5: {ID: 5, TaskID: &taskID, RequestID: "r5", Messages: []byte(`[{"role":"user"}]`), Result: []byte(`{"x":1}`)},
		}}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/api/llm/invocations/detail/5", nil)
		req.Header.Set("X-API-Key", "k")
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
		req.Header.Set("X-API-Key", "k")
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
