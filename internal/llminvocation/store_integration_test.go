//go:build integration

package llminvocation

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/dbtest"
	"github.com/V3teran/liusha/internal/task"
)

// setup 启动 Postgres、建一个 passive task 作为外键归属，返回 (Store, taskID)。
func setup(t *testing.T) (*Store, string) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	ts := task.NewStore(pool)
	tk, err := ts.Create(context.Background(), task.NewParams{
		AssignmentID: dbtest.SeedAssignment(t, pool),
		Brief:        "test brief",
		TargetHost:   "test.example.com",
	})
	if err != nil {
		t.Fatalf("create task: %v", err)
	}
	return NewStoreWithConfig(pool, config.InvocationConfig{}), tk.ID
}

// TestStore_Append_ListAll 验证：插一条调用 → flush → 读回，token/role 正确。
func TestStore_Append_ListAll(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	if _, err := s.Append(ctx, Invocation{
		TaskID:       &taskID,
		Provider:     "deepseek",
		Model:        "deepseek-chat",
		InTokens:     1200,
		OutTokens:    300,
		CachedTokens: 100,
		LatencyMs:    850,
		FinishReason: "stop",
		Role:         "planner",
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	rows, err := s.ListAll(ctx, ListFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("应读回 1 行，得到 %d", len(rows))
	}
	g := rows[0]
	if g.InTokens != 1200 || g.OutTokens != 300 || g.CachedTokens != 100 {
		t.Errorf("token 映射错: %+v", g)
	}
	if g.Role != "planner" {
		t.Errorf("role 错: %q", g.Role)
	}
	if g.RequestID == "" {
		t.Error("request_id 应由 db 侧 gen_random_uuid() 生成，不应为空")
	}
}

// TestStore_ListAll_Pagination 验证：offset 分页——limit 截断 + offset 取下一页，
// 不重不漏，且顺序按 id ASC（而非 created_at，同批 flush 的行 created_at 几乎相同不可靠）。
func TestStore_ListAll_Pagination(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	for i := 0; i < 3; i++ {
		if _, err := s.Append(ctx, Invocation{TaskID: &taskID, Provider: "deepseek", Model: "deepseek-chat", Role: "planner", InTokens: i}); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	page1, err := s.ListAll(ctx, ListFilter{Limit: 2})
	if err != nil {
		t.Fatalf("list page1: %v", err)
	}
	if len(page1) != 2 {
		t.Fatalf("第一页应 2 行，得到 %d", len(page1))
	}
	// ListAll 按,id DESC（全局搜索口径：最新在前）。
	if page1[0].InTokens != 2 || page1[1].InTokens != 1 {
		t.Errorf("第一页顺序错（应最新在前）: in_tokens=%d,%d", page1[0].InTokens, page1[1].InTokens)
	}

	page2, err := s.ListAll(ctx, ListFilter{Offset: 2, Limit: 2})
	if err != nil {
		t.Fatalf("list page2: %v", err)
	}
	if len(page2) != 1 {
		t.Fatalf("第二页应剩 1 行，得到 %d", len(page2))
	}
	if page2[0].InTokens != 0 {
		t.Errorf("第二页应是最旧一条(in_tokens=0)，得到 %d", page2[0].InTokens)
	}
}

// TestStore_GetDetailByID 验证：详情按 id 全局定位（搜索页钻取路径），能读到
// messages/result 原文；不存在的 id 报错而非静默零值。
func TestStore_GetDetailByID(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	if _, err := s.Append(ctx, Invocation{
		TaskID: &taskID, Provider: "deepseek", Model: "deepseek-chat", Role: "planner",
		Messages: []byte(`[{"role":"user","content":"hi"}]`),
		Result:   []byte(`{"content":"hello"}`),
	}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	rows, err := s.ListAll(ctx, ListFilter{})
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: rows=%d err=%v", len(rows), err)
	}
	id := rows[0].ID

	got, err := s.GetDetailByID(ctx, id)
	if err != nil {
		t.Fatalf("get detail: %v", err)
	}
	// jsonb 落库/读回会重排空白（"role":"user" → "role": "user"），语义比较而非字节比较。
	var gotMsgs, wantMsgs any
	if err := json.Unmarshal(got.Messages, &gotMsgs); err != nil {
		t.Fatalf("messages 非法 json: %s", got.Messages)
	}
	_ = json.Unmarshal([]byte(`[{"role":"user","content":"hi"}]`), &wantMsgs)
	if !reflect.DeepEqual(gotMsgs, wantMsgs) {
		t.Errorf("messages 内容不符: %s", got.Messages)
	}
	var gotResult, wantResult any
	if err := json.Unmarshal(got.Result, &gotResult); err != nil {
		t.Fatalf("result 非法 json: %s", got.Result)
	}
	_ = json.Unmarshal([]byte(`{"content":"hello"}`), &wantResult)
	if !reflect.DeepEqual(gotResult, wantResult) {
		t.Errorf("result 内容不符: %s", got.Result)
	}

	if _, err := s.GetDetailByID(ctx, 0); err == nil {
		t.Error("不存在的 id 应报错，却返回了行")
	}
}

// TestStore_AggregateByTask 验证：插多条 → 合计 token/latency/calls 等于各项之和。
func TestStore_AggregateByTask(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	rows := []Invocation{
		{Provider: "deepseek", Model: "deepseek-chat", Role: "planner", InTokens: 100, OutTokens: 10, CachedTokens: 5, LatencyMs: 200},
		{Provider: "deepseek", Model: "deepseek-chat", Role: "executor", InTokens: 200, OutTokens: 20, CachedTokens: 0, LatencyMs: 300},
		{Provider: "deepseek", Model: "deepseek-chat", Role: "executor", InTokens: 50, OutTokens: 5, CachedTokens: 50, LatencyMs: 100},
	}
	for i := range rows {
		rows[i].TaskID = &taskID
		if _, err := s.Append(ctx, rows[i]); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	agg, err := s.AggregateByTask(ctx, taskID, ListFilter{})
	if err != nil {
		t.Fatalf("aggregate: %v", err)
	}
	if agg.Calls != 3 {
		t.Errorf("calls 应为 3, got %d", agg.Calls)
	}
	if agg.InTokens != 350 || agg.OutTokens != 35 || agg.CachedTokens != 55 {
		t.Errorf("token 合计错: %+v", agg)
	}
	if agg.LatencyMs != 600 {
		t.Errorf("latency 合计应为 600, got %d", agg.LatencyMs)
	}
}

// seedFiltered 插入一组差异化的调用（role/model/error/时间各不同），供筛选类测试复用。
func seedFiltered(t *testing.T, s *Store, taskID string) {
	t.Helper()
	ctx := context.Background()
	rows := []Invocation{
		{Role: "planner", Model: "glm-5.3-flash", InTokens: 100, OutTokens: 10},
		{Role: "executor", Model: "glm-5.3-flash", InTokens: 200, OutTokens: 20},
		{Role: "executor", Model: "deepseek-chat", InTokens: 400, OutTokens: 40, Error: "429 rate limited"},
	}
	for i := range rows {
		rows[i].TaskID = &taskID
		rows[i].Provider = "x"
		if _, err := s.Append(ctx, rows[i]); err != nil {
			t.Fatalf("append %d: %v", i, err)
		}
	}
	if err := s.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
}

// TestStore_ListAll_Filters 验证 role/model/仅错误 三个筛选维度各自生效。
func TestStore_ListAll_Filters(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)
	seedFiltered(t, s, taskID)

	cases := []struct {
		name string
		f    ListFilter
		want int
	}{
		{"不筛=全量", ListFilter{}, 3},
		{"按 role", ListFilter{Role: "executor"}, 2},
		{"按 model", ListFilter{Model: "glm-5.3-flash"}, 2},
		{"仅错误", ListFilter{OnlyErr: true}, 1},
		{"role+model 交集", ListFilter{Role: "executor", Model: "glm-5.3-flash"}, 1},
		{"无匹配", ListFilter{Role: "nobody"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rows, err := s.ListAll(ctx, c.f)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(rows) != c.want {
				t.Errorf("命中 %d 行，期望 %d（filter=%+v）", len(rows), c.want, c.f)
			}
		})
	}
}

// TestStore_AggregateByTask_Filtered 验证统计吃筛选（与列表同源），且不受分页游标影响——
// 否则会出现「明细筛剩 1 条、合计仍是全量」的自相矛盾。
func TestStore_AggregateByTask_Filtered(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)
	seedFiltered(t, s, taskID)

	agg, err := s.AggregateByTask(ctx, taskID, ListFilter{Role: "executor"})
	if err != nil {
		t.Fatalf("aggregate filtered: %v", err)
	}
	if agg.Calls != 2 || agg.InTokens != 600 || agg.OutTokens != 60 {
		t.Errorf("按 role 筛选后合计错: %+v（期望 calls=2 in=600 out=60）", agg)
	}

	// 分页（Offset/Limit）只属于列表；统计是整个筛选结果集的合计，与翻页无关。
	withPaging, err := s.AggregateByTask(ctx, taskID, ListFilter{Offset: 1, Limit: 1})
	if err != nil {
		t.Fatalf("aggregate with paging: %v", err)
	}
	if withPaging.Calls != 3 {
		t.Errorf("统计不该受分页影响，got calls=%d，期望 3", withPaging.Calls)
	}
}

// TestStore_AggregateByTask_Empty 验证：无任何调用时返回零值，不报错。
func TestStore_AggregateByTask_Empty(t *testing.T) {
	ctx := context.Background()
	s, taskID := setup(t)

	agg, err := s.AggregateByTask(ctx, taskID, ListFilter{})
	if err != nil {
		t.Fatalf("aggregate on empty: %v", err)
	}
	if agg.Calls != 0 || agg.InTokens != 0 || agg.LatencyMs != 0 {
		t.Fatalf("空 task 应全零, got %+v", agg)
	}
}
