package einotools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"

	"github.com/V3teran/liusha/internal/flow"
)

// fakeFlowLister 实现 FlowLister + FlowReader（view_flow 用 GetByID）。
type fakeFlowLister struct {
	rows      []flow.FlowSummary
	gotFilter flow.ListFilter
	viewFlow  flow.Flow
}

func (f *fakeFlowLister) ListByOwnerFiltered(_ context.Context, _ string, filter flow.ListFilter) ([]flow.FlowSummary, error) {
	f.gotFilter = filter
	return f.rows, nil
}
func (f *fakeFlowLister) GetByID(_ context.Context, _ int64) (flow.Flow, error) {
	return f.viewFlow, nil
}

func TestListFlows_DefaultHostAndStripPort(t *testing.T) {
	store := &fakeFlowLister{rows: []flow.FlowSummary{
		{ID: 1, Method: "GET", Host: "target.com", Path: "/admin", StatusCode: 200},
	}}
	lf, err := BuildListFlows(store, "active_scan", "owner-1", "target.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, lf, `{}`) // 不传 host → 默认 hunterHost，剥端口
	if !strings.Contains(out, "/admin") {
		t.Fatalf("输出缺 flow: %s", out)
	}
	// host 默认取 hunterHost 且剥端口 → "target.com"
	if store.gotFilter.Host != "target.com" {
		t.Errorf("host 应默认剥端口为 target.com，得到 %q", store.gotFilter.Host)
	}
}

func TestListFlows_LimitClamp(t *testing.T) {
	store := &fakeFlowLister{}
	lf, _ := BuildListFlows(store, "active_scan", "owner-1", "h")
	invoke(t, lf, `{"limit":9999,"method":"post"}`)
	if store.gotFilter.Limit != listFlowsMaxLimit {
		t.Errorf("limit 应钳到 %d，得到 %d", listFlowsMaxLimit, store.gotFilter.Limit)
	}
	if store.gotFilter.Method != "POST" {
		t.Errorf("method 应大写: %q", store.gotFilter.Method)
	}
}

func TestListFlows_MissingOwner(t *testing.T) {
	lf, _ := BuildListFlows(&fakeFlowLister{}, "active_scan", "", "h")
	it := lf.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{}`); err == nil {
		t.Fatal("owner 注入缺失应报错")
	}
}

func TestViewFlow_OwnerIsolation(t *testing.T) {
	store := &fakeFlowLister{viewFlow: flow.Flow{ID: 5, OwnerID: "other", Method: "GET"}}
	vf, err := BuildViewFlow(store, "active_scan", "owner-1")
	if err != nil {
		t.Fatal(err)
	}
	it := vf.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"id":5}`); err == nil || !strings.Contains(err.Error(), "跨 owner") {
		t.Fatalf("跨 owner view 应被拒，得到: %v", err)
	}
}

func TestViewFlow_ReturnsFull(t *testing.T) {
	store := &fakeFlowLister{viewFlow: flow.Flow{
		ID: 5, OwnerID: "owner-1", Method: "POST", URL: "http://t/login",
		RequestBody: []byte("user=admin"), StatusCode: 200,
	}}
	vf, _ := BuildViewFlow(store, "active_scan", "owner-1")
	out := invoke(t, vf, `{"id":5}`)
	if !strings.Contains(out, "user=admin") || !strings.Contains(out, "/login") {
		t.Fatalf("view 应返回完整请求: %s", out)
	}
}
