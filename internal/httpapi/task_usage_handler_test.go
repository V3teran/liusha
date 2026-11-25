package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/llminvocation"
	"github.com/V3teran/liusha/internal/toolinvocation"
)

type fakeLLMUsage struct {
	agg     llminvocation.Aggregate
	flushed bool
	gotTask string
}

func (f *fakeLLMUsage) Flush(context.Context) error { f.flushed = true; return nil }

func (f *fakeLLMUsage) AggregateAll(_ context.Context, filter llminvocation.ListFilter) (llminvocation.Aggregate, error) {
	f.gotTask = filter.TaskID
	return f.agg, nil
}

type fakeToolUsage struct{ agg toolinvocation.Aggregate }

func (f *fakeToolUsage) AggregateByTask(context.Context, string) (toolinvocation.Aggregate, error) {
	return f.agg, nil
}

// 用量属于任务：按任务 ID 合计 LLM 与工具调用，耗时取任务的墙钟（扣除续接前的停顿）。
func TestGetTaskUsage(t *testing.T) {
	created := time.Date(2026, 11, 15, 10, 0, 0, 0, time.UTC)
	ended := created.Add(10 * time.Minute)
	llm := &fakeLLMUsage{agg: llminvocation.Aggregate{Calls: 3, InTokens: 1200, OutTokens: 300, CachedTokens: 800, LatencyMs: 4000}}
	h := NewServer(Deps{
		Auth:       newFakeAuthStore(),
		TaskReader: fakeTaskReader{graphTaskID: {ID: graphTaskID, CreatedAt: created, EndedAt: &ended, PausedMs: 60_000}},
		UsageLLM:   llm,
		UsageTools: &fakeToolUsage{agg: toolinvocation.Aggregate{Calls: 2, DurationMs: 1500}},
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
