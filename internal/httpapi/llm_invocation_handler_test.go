package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/llminvocation"
)

// fakeInvocations 是 InvocationsAPI 的内存实现，用于路由级单测。
type fakeInvocations struct {
	rows      []llminvocation.Invocation
	byID      map[int64]llminvocation.Invocation
	agg       llminvocation.Aggregate
	facets    llminvocation.Facets
	err       error
	gotFilter llminvocation.ListFilter // 列表收到的筛选（断言 query 解析）
	gotAggF   llminvocation.ListFilter // 统计收到的筛选（断言与列表同源）
}

func (f *fakeInvocations) Flush(_ context.Context) error { return nil }

func (f *fakeInvocations) ListByTask(_ context.Context, _ string, filter llminvocation.ListFilter) ([]llminvocation.Invocation, error) {
	f.gotFilter = filter
	if f.err != nil {
		return nil, f.err
	}
	var out []llminvocation.Invocation
	for _, r := range f.rows {
		if r.ID > filter.AfterID {
			out = append(out, r)
		}
	}
	if filter.Limit > 0 && len(out) > filter.Limit {
		out = out[:filter.Limit]
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

func (f *fakeInvocations) AggregateByTask(_ context.Context, _ string, filter llminvocation.ListFilter) (llminvocation.Aggregate, error) {
	f.gotAggF = filter
	return f.agg, f.err
}

func (f *fakeInvocations) FacetsByTask(_ context.Context, _ string) (llminvocation.Facets, error) {
	return f.facets, f.err
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
			Items     []struct {
				ID        int64  `json:"id"`
				RequestID string `json:"request_id"`
				Role      string `json:"role"`
			} `json:"items"`
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
			t.Errorf("next_after=%d，期望 2（本页最后一行 id）", body.NextAfter)
		}
		if fake.gotFilter.Limit != 2 {
			t.Errorf("limit 未透传，got %d", fake.gotFilter.Limit)
		}
		// 扁平 items（不再按 hunter 分组），request_id/role 随行返回。
		if len(body.Items) != 2 {
			t.Fatalf("items 不符: %+v", body.Items)
		}
		if body.Items[0].RequestID == "" {
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

		if fake.gotFilter.Limit != defaultInvocationPageSize {
			t.Errorf("超上限应收敛到默认值 %d，got %d", defaultInvocationPageSize, fake.gotFilter.Limit)
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
		inv := raw["items"].([]any)[0].(map[string]any)
		if _, ok := inv["messages"]; ok {
			t.Error("列表响应不该带 messages（大字段，详情端点才有）")
		}
		if _, ok := inv["result"]; ok {
			t.Error("列表响应不该带 result（大字段，详情端点才有）")
		}
	})

	t.Run("筛选参数解析：role/model/only_err/时间范围", func(t *testing.T) {
		fake := &fakeInvocations{}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET",
			srv.URL+"/llm/invocations/t1?role=orchestrator&model=mimo-v2.5&only_err=1"+
				"&start=2026-07-20T00:00:00Z&end=2026-07-21T00:00:00Z&after=7", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		f := fake.gotFilter
		if f.Role != "orchestrator" || f.Model != "mimo-v2.5" || !f.OnlyErr {
			t.Errorf("role/model/only_err 解析错: %+v", f)
		}
		if f.AfterID != 7 {
			t.Errorf("after 未解析，got %d", f.AfterID)
		}
		if f.Start == nil || f.Start.UTC().Format(time.RFC3339) != "2026-07-20T00:00:00Z" {
			t.Errorf("start 解析错: %v", f.Start)
		}
		if f.End == nil || f.End.UTC().Format(time.RFC3339) != "2026-07-21T00:00:00Z" {
			t.Errorf("end 解析错: %v", f.End)
		}
	})

	t.Run("非法时间参数退化为不筛，不返 400", func(t *testing.T) {
		fake := &fakeInvocations{}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/llm/invocations/t1?start=not-a-time", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			t.Errorf("状态码=%d，期望 200（坏参数退化为不筛）", resp.StatusCode)
		}
		if fake.gotFilter.Start != nil {
			t.Error("非法 start 应视为未传")
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

	// 统计必须吃与列表同一套筛选，否则会出现「明细筛剩 3 条、合计仍是全量」的自相矛盾。
	t.Run("统计吃同一套筛选，但不吃分页游标", func(t *testing.T) {
		fake := &fakeInvocations{}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET",
			srv.URL+"/llm/invocations/t1/stat?role=exploitation&model=mimo-v2.5&only_err=1&after=99", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		f := fake.gotAggF
		if f.Role != "exploitation" || f.Model != "mimo-v2.5" || !f.OnlyErr {
			t.Errorf("统计未收到筛选条件: %+v", f)
		}
		if f.AfterID != 0 {
			t.Errorf("统计不该受分页游标影响，got AfterID=%d", f.AfterID)
		}
	})
}

func TestLLMInvocationFacetsHandler(t *testing.T) {
	t.Run("200 返回 role/model 候选", func(t *testing.T) {
		fake := &fakeInvocations{facets: llminvocation.Facets{
			Roles:  []string{"exploitation", "orchestrator"},
			Models: []string{"mimo-v2.5"},
		}}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/llm/invocations/t1/facets", nil)
		req.Header.Set("X-API-Key", "k")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()

		var body struct {
			Roles  []string `json:"roles"`
			Models []string `json:"models"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if len(body.Roles) != 2 || body.Roles[0] != "exploitation" {
			t.Errorf("roles 不符: %+v", body.Roles)
		}
		if len(body.Models) != 1 || body.Models[0] != "mimo-v2.5" {
			t.Errorf("models 不符: %+v", body.Models)
		}
	})

	// nil slice 会序列化成 null，前端得多写判空；统一成 [] 更省事。
	t.Run("空候选序列化成 [] 而非 null", func(t *testing.T) {
		fake := &fakeInvocations{}
		srv := newTestServer(t, Deps{Invocations: fake})
		defer srv.Close()

		req, _ := http.NewRequest("GET", srv.URL+"/llm/invocations/t1/facets", nil)
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
		if raw["roles"] == nil || raw["models"] == nil {
			t.Errorf("空候选应为 []，得到 null: %+v", raw)
		}
	})
}
