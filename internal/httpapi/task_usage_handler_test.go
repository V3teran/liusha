package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/toolinvocation"
)

type fakeLLMUsage struct {
	agg     llminvocation.Aggregate
	byRole  []llminvocation.RoleAggregate
	flushed bool
	gotTask string
}

func (f *fakeLLMUsage) Flush(context.Context) error { f.flushed = true; return nil }

func (f *fakeLLMUsage) AggregateAll(_ context.Context, filter llminvocation.ListFilter) (llminvocation.Aggregate, error) {
	f.gotTask = filter.TaskID
	return f.agg, nil
}

func (f *fakeLLMUsage) AggregateByRole(context.Context, llminvocation.ListFilter) ([]llminvocation.RoleAggregate, error) {
	return f.byRole, nil
}

type fakeToolUsage struct {
	agg    toolinvocation.Aggregate
	byRole []toolinvocation.RoleAggregate
}

func (f *fakeToolUsage) AggregateByTask(context.Context, string) (toolinvocation.Aggregate, error) {
	return f.agg, nil
}

func (f *fakeToolUsage) AggregateByTaskRole(context.Context, string) ([]toolinvocation.RoleAggregate, error) {
	return f.byRole, nil
}

// 模型调用与工具调用两份按角色的分组合成一份：只在一侧出现的主角照列，调用多的排前。
func TestMergeRoleUsage(t *testing.T) {
	got := mergeRoleUsage(
		[]llminvocation.RoleAggregate{
			{Role: "compaction", Aggregate: llminvocation.Aggregate{Calls: 1, InTokens: 10}},
			{Role: "executor", Aggregate: llminvocation.Aggregate{Calls: 4, InTokens: 100, OutTokens: 20, LatencyMs: 900}},
			{Role: "planner", Aggregate: llminvocation.Aggregate{Calls: 2, InTokens: 50}},
		},
		[]toolinvocation.RoleAggregate{
			{Role: "executor", Aggregate: toolinvocation.Aggregate{Calls: 5, DurationMs: 700}},
			{Role: "monitor", Aggregate: toolinvocation.Aggregate{Calls: 1, DurationMs: 5}},
		},
	)
	want := []roleUsage{
		{Role: "executor", Tokens: tokenUsage{In: 100, Out: 20}, LLMInvocations: 4, LLMLatencyMs: 900, ToolInvocations: 5, ToolDurationMs: 700},
		{Role: "planner", Tokens: tokenUsage{In: 50}, LLMInvocations: 2},
		// 历史压缩与监察者的调用一样多（各 1 次），按角色名排
		{Role: "compaction", Tokens: tokenUsage{In: 10}, LLMInvocations: 1},
		{Role: "monitor", ToolInvocations: 1, ToolDurationMs: 5},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("按调用方分组合计不符：\n得   %#v\n期望 %#v", got, want)
	}
	// 一次调用都没有：空表（JSON 里是 []），不是 null
	if got := mergeRoleUsage(nil, nil); got == nil || len(got) != 0 {
		t.Errorf("没有调用时应为空表，得 %#v", got)
	}
}

// 用量属于任务：按任务 ID 合计 LLM 与工具调用，耗时取任务的墙钟（扣除续接前的停顿）；
// 同一批调用另按调用方分开一份。
func TestGetTaskUsage(t *testing.T) {
	created := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	ended := created.Add(10 * time.Minute)
	llm := &fakeLLMUsage{
		agg: llminvocation.Aggregate{Calls: 3, InTokens: 1200, OutTokens: 300, CachedTokens: 800, LatencyMs: 4000},
		// 与合计同一批调用拆开：规划者 1 次、执行者 2 次
		byRole: []llminvocation.RoleAggregate{
			{Role: "planner", Aggregate: llminvocation.Aggregate{Calls: 1, InTokens: 400, OutTokens: 100, CachedTokens: 300, LatencyMs: 1500}},
			{Role: "executor", Aggregate: llminvocation.Aggregate{Calls: 2, InTokens: 800, OutTokens: 200, CachedTokens: 500, LatencyMs: 2500}},
		},
	}
	h := NewServer(Deps{
		Auth:       newFakeAuthStore(),
		TaskReader: fakeTaskReader{graphTaskID: {ID: graphTaskID, CreatedAt: created, EndedAt: &ended, PausedMs: 60_000}},
		UsageLLM:   llm,
		UsageTools: &fakeToolUsage{
			agg:    toolinvocation.Aggregate{Calls: 2, DurationMs: 1500},
			byRole: []toolinvocation.RoleAggregate{{Role: "executor", Aggregate: toolinvocation.Aggregate{Calls: 2, DurationMs: 1500}}},
		},
	})

	w := getAuthed(t, h, "/api/tasks/"+graphTaskID+"/usage")
	if w.Code != http.StatusOK {
		t.Fatalf("状态码 %d：%s", w.Code, w.Body.String())
	}
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{
		"tokens":           map[string]any{"in": 1200.0, "out": 300.0, "cached": 800.0},
		"llm_calls":        3.0,
		"llm_latency_ms":   4000.0,
		"tool_calls":       2.0,
		"tool_duration_ms": 1500.0,
		"elapsed_ms":       float64((9 * time.Minute).Milliseconds()),
		// 执行者的调用多，排在规划者前面；规划者没调工具
		"by_role": []any{
			map[string]any{
				"role": "executor", "tokens": map[string]any{"in": 800.0, "out": 200.0, "cached": 500.0},
				"llm_calls": 2.0, "llm_latency_ms": 2500.0, "tool_calls": 2.0, "tool_duration_ms": 1500.0,
			},
			map[string]any{
				"role": "planner", "tokens": map[string]any{"in": 400.0, "out": 100.0, "cached": 300.0},
				"llm_calls": 1.0, "llm_latency_ms": 1500.0, "tool_calls": 0.0, "tool_duration_ms": 0.0,
			},
		},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Errorf("响应\n得   %s\n期望 %s", gotJSON, wantJSON)
	}
	if !llm.flushed || llm.gotTask != graphTaskID {
		t.Errorf("应先落盘缓冲再按任务合计，flushed=%v task=%q", llm.flushed, llm.gotTask)
	}
}

func TestGetTaskUsage_UnknownTaskIs404(t *testing.T) {
	h := NewServer(Deps{
		Auth:       newFakeAuthStore(),
		TaskReader: fakeTaskReader{},
		UsageLLM:   &fakeLLMUsage{},
		UsageTools: &fakeToolUsage{},
	})
	if w := getAuthed(t, h, "/api/tasks/"+graphTaskID+"/usage"); w.Code != http.StatusNotFound {
		t.Fatalf("任务不存在应 404，得 %d", w.Code)
	}
}

// 对话下的用量接口已删除：状态与用量都属于任务。
func TestConversationUsageRouteRemoved(t *testing.T) {
	h := NewServer(Deps{
		Auth:       newFakeAuthStore(),
		TaskReader: fakeTaskReader{graphTaskID: {ID: graphTaskID}},
		UsageLLM:   &fakeLLMUsage{},
		UsageTools: &fakeToolUsage{},
	})
	if w := getAuthed(t, h, "/api/conversations/"+graphTaskID+"/usage"); w.Code != http.StatusNotFound {
		t.Fatalf("应已删除（404），得 %d", w.Code)
	}
}
