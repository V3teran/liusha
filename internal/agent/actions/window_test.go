package actions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/window"
)

// 编译期接口断言：确认真实 *window.Store / *flow.Store 能直接喂给 ReadWindow。
// 任何签名漂移会在 build/test 阶段立刻暴露。
var (
	_ WindowStore = (*window.Store)(nil)
	_ FlowReader  = (*flow.Store)(nil)
)

// fakeWinStore 是 WindowStore 的内存实现：记录 GetByID 命中与 MarkConsumed 调用。
type fakeWinStore struct {
	w        window.Window
	getErr   error
	consumed []string
}

func (f *fakeWinStore) GetByID(_ context.Context, _ string) (window.Window, error) {
	if f.getErr != nil {
		return window.Window{}, f.getErr
	}
	return f.w, nil
}

func (f *fakeWinStore) MarkConsumed(_ context.Context, id string) error {
	f.consumed = append(f.consumed, id)
	return nil
}

// fakeFlowStore 是 FlowReader 的内存实现，用 map 模拟 id → flow 的反查。
type fakeFlowStore struct {
	flows map[int64]flow.Flow
}

func (f *fakeFlowStore) GetByID(_ context.Context, id int64) (flow.Flow, error) {
	v, ok := f.flows[id]
	if !ok {
		return flow.Flow{}, errors.New("not found")
	}
	return v, nil
}

// ---------- ReadWindow ----------

func TestReadWindow_ReturnsFlowsAndMarksConsumed(t *testing.T) {
	wins := &fakeWinStore{w: window.Window{
		ID:    "w1",
		Flows: []window.FlowRef{{ID: 10}, {ID: 11}},
	}}
	flows := &fakeFlowStore{flows: map[int64]flow.Flow{
		10: {ID: 10, Method: "GET", URL: "/api/order/7", StatusCode: 200},
		11: {ID: 11, Method: "POST", URL: "/api/order/cancel", StatusCode: 200},
	}}
	a := &ReadWindow{Windows: wins, Flows: flows}

	out, err := a.Execute(context.Background(), json.RawMessage(`{"window_id":"w1"}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}

	var got struct {
		WindowID string           `json:"window_id"`
		Flows    []map[string]any `json:"flows"`
		Consumed bool             `json:"consumed"`
	}
	if err := json.Unmarshal(out.Output, &got); err != nil {
		t.Fatalf("output 解析失败: %v", err)
	}
	if got.WindowID != "w1" {
		t.Fatalf("window_id 未透传: %q", got.WindowID)
	}
	if len(got.Flows) != 2 {
		t.Fatalf("应返回 2 条 flow 摘要，got %d", len(got.Flows))
	}
	if got.Flows[0]["method"] != "GET" || got.Flows[0]["url"] != "/api/order/7" {
		t.Fatalf("第 1 条 flow 摘要不对: %+v", got.Flows[0])
	}
	if !got.Consumed {
		t.Fatalf("consumed 应为 true")
	}
	if len(wins.consumed) != 1 || wins.consumed[0] != "w1" {
		t.Fatalf("MarkConsumed 应被调用 1 次（w1），got %v", wins.consumed)
	}
}

func TestReadWindow_WindowNotFound(t *testing.T) {
	wins := &fakeWinStore{getErr: errors.New("no rows")}
	a := &ReadWindow{Windows: wins, Flows: &fakeFlowStore{}}
	_, err := a.Execute(context.Background(), json.RawMessage(`{"window_id":"missing"}`))
	if err == nil {
		t.Fatal("窗口不存在应返回 error")
	}
	if len(wins.consumed) != 0 {
		t.Fatalf("窗口不存在不应调用 MarkConsumed，got %v", wins.consumed)
	}
}

func TestReadWindow_RejectsEmptyWindowID(t *testing.T) {
	a := &ReadWindow{Windows: &fakeWinStore{}, Flows: &fakeFlowStore{}}
	_, err := a.Execute(context.Background(), json.RawMessage(`{"window_id":""}`))
	if err == nil {
		t.Fatal("空 window_id 应报错")
	}
}

func TestReadWindow_SkipsMissingFlowAndStillConsumes(t *testing.T) {
	// flow 11 不在 fakeFlowStore，应跳过但不影响窗口本身被标记 consumed。
	wins := &fakeWinStore{w: window.Window{
		ID:    "w2",
		Flows: []window.FlowRef{{ID: 10}, {ID: 11}},
	}}
	flows := &fakeFlowStore{flows: map[int64]flow.Flow{
		10: {ID: 10, Method: "GET", URL: "/api/x", StatusCode: 200},
	}}
	a := &ReadWindow{Windows: wins, Flows: flows}

	out, err := a.Execute(context.Background(), json.RawMessage(`{"window_id":"w2"}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	var got struct {
		Flows []map[string]any `json:"flows"`
	}
	_ = json.Unmarshal(out.Output, &got)
	if len(got.Flows) != 1 {
		t.Fatalf("应只返回 1 条可用 flow，got %d", len(got.Flows))
	}
	if len(wins.consumed) != 1 {
		t.Fatalf("应仍调用 1 次 MarkConsumed，got %v", wins.consumed)
	}
}

func TestReadWindow_SummaryWithin200Chars(t *testing.T) {
	wins := &fakeWinStore{w: window.Window{
		ID:    "w3",
		Flows: []window.FlowRef{{ID: 1}, {ID: 2}, {ID: 3}},
	}}
	flows := &fakeFlowStore{flows: map[int64]flow.Flow{
		1: {ID: 1, Method: "GET", URL: "/api/a", StatusCode: 200},
		2: {ID: 2, Method: "POST", URL: "/api/b", StatusCode: 401},
		3: {ID: 3, Method: "DELETE", URL: "/api/c", StatusCode: 500},
	}}
	a := &ReadWindow{Windows: wins, Flows: flows}
	out, err := a.Execute(context.Background(), json.RawMessage(`{"window_id":"w3"}`))
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if l := len([]rune(out.Summary)); l == 0 || l > 200 {
		t.Fatalf("Summary 长度应在 (0,200]，got %d: %q", l, out.Summary)
	}
}

func TestReadWindow_RejectsMalformedArgs(t *testing.T) {
	a := &ReadWindow{Windows: &fakeWinStore{}, Flows: &fakeFlowStore{}}
	_, err := a.Execute(context.Background(), json.RawMessage(`{not-json`))
	if err == nil {
		t.Fatal("非法 JSON 应返回错误")
	}
}
