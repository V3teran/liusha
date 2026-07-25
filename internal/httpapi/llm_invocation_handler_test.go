package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/V3teran/liusha/internal/llminvocation"
)

// fakeInvocations 是 InvocationsAPI 的内存实现，用于路由级单测。
type fakeInvocations struct {
	rows       []llminvocation.Invocation
	byID       map[int64]llminvocation.Invocation
	agg        llminvocation.Aggregate
	err        error
	gotAfterID int64
	gotLimit   int
}

func (f *fakeInvocations) Flush(_ context.Context) error { return nil }

func (f *fakeInvocations) ListByTask(_ context.Context, _ string, afterID int64, limit int) ([]llminvocation.Invocation, error) {
	f.gotAfterID, f.gotLimit = afterID, limit
	if f.err != nil {
		return nil, f.err
	}
	var out []llminvocation.Invocation
	for _, r := range f.rows {
		if r.ID > afterID {
			out = append(out, r)
		}
	}
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeInvocations) GetByID(_ context.Context, taskID string, id int64) (llminvocation.Invocation, error) {
	v, ok := f.byID[id]
	if !ok || (v.TaskID != nil && *v.TaskID != taskID) {
		return llminvocation.Invocation{}, errors.New("no rows in result set")
	}
	return v, nil
}

func (f *fakeInvocations) AggregateByTask(_ context.Context, _ string) (llminvocation.Aggregate, error) {
	return f.agg, f.err
}

func TestLLMInvocationsHandler(t *testing.T) {
	t.Run("列表分页：limit 截断 + next_after/has_more", func(t *testing.T) {
		taskID := "t1"
		fake := &fakeInvocations{rows: []llminvocation.Invocation{
			{ID: 1, TaskID: &taskID, RequestID: "r1", Role: "orchestrator"},
			{ID: 2, TaskID: &taskID, RequestID: "r2", Role: "exploitation"},
			{ID: 3, TaskID: &taskID, RequestID: "r3", Role: "exploitation"},
		}}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/llm/invocations/t1?limit=2", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var body struct {
			Total     int   `json:"total"`
			NextAfter int64 `json:"next_after"`
			HasMore   bool  `json:"has_more"`
			Groups    []struct {
				HunterID    string `json:"hunter_id"`
				Count       int    `json:"count"`
				Invocations []struct {
					ID        int64  `json:"id"`
					RequestID string `json:"request_id"`
					Role      string `json:"role"`
				} `json:"invocations"`
			} `json:"groups"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Total != 2 {
			t.Errorf("total=%d，期望 2（limit 截断）", body.Total)
		}
		if !body.HasMore {
			t.Error("拉满一页应 has_more=true")
		}
		if body.NextAfter != 2 {
			t.Errorf("next_after=%d，期望 2（第二页最后一行 id）", body.NextAfter)
		}
		if fake.gotLimit != 2 {
			t.Errorf("limit 未透传，got %d", fake.gotLimit)
		}
		// unassigned 分组下应看到两行，且 request_id/role 都在（无 messages/result 大字段）。
		if len(body.Groups) != 1 || len(body.Groups[0].Invocations) != 2 {
			t.Fatalf("分组不符: %+v", body.Groups)
		}
		if body.Groups[0].Invocations[0].RequestID == "" {
			t.Error("列表响应应带 request_id")
		}
	})

	t.Run("limit 缺省/超上限收敛到默认值", func(t *testing.T) {
		fake := &fakeInvocations{}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/llm/invocations/t1?limit=99999", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if fake.gotLimit != defaultInvocationPageSize {
			t.Errorf("超上限应收敛到默认值 %d，got %d", defaultInvocationPageSize, fake.gotLimit)
		}
	})

	t.Run("列表响应不含 messages/result 大字段", func(t *testing.T) {
		taskID := "t1"
		fake := &fakeInvocations{rows: []llminvocation.Invocation{
			{ID: 1, TaskID: &taskID, Messages: []byte(`[{"role":"user"}]`), Result: []byte(`{"x":1}`)},
		}}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/llm/invocations/t1", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var raw map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		groups := raw["groups"].([]any)
		inv := groups[0].(map[string]any)["invocations"].([]any)[0].(map[string]any)
		if _, ok := inv["messages"]; ok {
			t.Error("列表响应不该带 messages（大字段，详情端点才有）")
		}
		if _, ok := inv["result"]; ok {
			t.Error("列表响应不该带 result（大字段，详情端点才有）")
		}
	})
}

func TestLLMInvocationDetailHandler(t *testing.T) {
	t.Run("200 返回完整行含 messages/result", func(t *testing.T) {
		taskID := "t1"
		fake := &fakeInvocations{byID: map[int64]llminvocation.Invocation{
			5: {ID: 5, TaskID: &taskID, RequestID: "r5", Messages: []byte(`[{"role":"user"}]`), Result: []byte(`{"x":1}`)},
		}}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/llm/invocations/t1/invocation/5", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

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

	t.Run("跨 task 猜 id 应 404（不越权）", func(t *testing.T) {
		other := "t2"
		fake := &fakeInvocations{byID: map[int64]llminvocation.Invocation{
			5: {ID: 5, TaskID: &other},
		}}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/llm/invocations/t1/invocation/5", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 404 {
			t.Errorf("状态码=%d，期望 404", resp.StatusCode)
		}
	})
}

func TestLLMInvocationStatHandler(t *testing.T) {
	t.Run("200 返回聚合统计", func(t *testing.T) {
		fake := &fakeInvocations{agg: llminvocation.Aggregate{Calls: 3, InTokens: 350, OutTokens: 35, CachedTokens: 55, LatencyMs: 600}}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/llm/invocations/t1/stat", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var body struct {
			Calls    int   `json:"calls"`
			InTokens int64 `json:"in_tokens"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body.Calls != 3 || body.InTokens != 350 {
			t.Errorf("聚合结果不符: %+v", body)
		}
	})
}
