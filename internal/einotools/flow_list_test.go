package einotools

import (
	"context"
	"strings"
	"testing"

	"github.com/cloudwego/eino/components/tool"
)

// fakeFlowScope 同时满足 FlowLister（ListInScope）+ FlowReader（GetInScope）。
// task 范围隔离已收敛到适配器（GetInScope 返回 ok=false 表越界），fake 直接用 viewOK 模拟。
type fakeFlowScope struct {
	rows     []FlowSummaryRecord
	gotQuery FlowQuery
	viewFlow FlowRecord
	viewOK   bool
}

func (f *fakeFlowScope) ListInScope(_ context.Context, q FlowQuery) ([]FlowSummaryRecord, error) {
	f.gotQuery = q
	return f.rows, nil
}
func (f *fakeFlowScope) GetInScope(_ context.Context, _ int64) (FlowRecord, bool, error) {
	return f.viewFlow, f.viewOK, nil
}

func TestListFlows_DefaultHostFromHunterHost(t *testing.T) {
	store := &fakeFlowScope{rows: []FlowSummaryRecord{
		{ID: 1, Method: "GET", Host: "target.com:8080", Path: "/admin", StatusCode: 200},
	}}
	lf, err := BuildListFlows(store, "target.com:8080")
	if err != nil {
		t.Fatal(err)
	}
	out := invoke(t, lf, `{}`) // 不传 host → 默认 hunterHost
	if !strings.Contains(out, "/admin") {
		t.Fatalf("输出缺 flow: %s", out)
	}
	// host 默认取 hunterHost，原样透传（不剥端口——流量表 host 列存的就是 host:port，
	// 剥了就和存储值对不上，过滤永远零命中，见 2026-07-14 e2e 实测的 bug）。
	if store.gotQuery.Host != "target.com:8080" {
		t.Errorf("host 应原样透传 target.com:8080，得到 %q", store.gotQuery.Host)
	}
}

func TestListFlows_LimitClamp(t *testing.T) {
	store := &fakeFlowScope{}
	lf, _ := BuildListFlows(store, "h")
	invoke(t, lf, `{"limit":9999,"method":"post"}`)
	if store.gotQuery.Limit != listFlowsMaxLimit {
		t.Errorf("limit 应钳到 %d，得到 %d", listFlowsMaxLimit, store.gotQuery.Limit)
	}
	if store.gotQuery.Method != "POST" {
		t.Errorf("method 应大写: %q", store.gotQuery.Method)
	}
}

// 旧 TestListFlows_MissingOwner 删除：owner 注入校验随 owner 概念坍缩移除，
// task 范围现由 BuildListFlows 注入的 scope 适配器闭包绑定，工具零分支、无缺失可校验。

func TestViewFlow_ScopeIsolation(t *testing.T) {
	// 越界流量：适配器返回 ok=false（原 OwnerID 跨 owner 判定移到 GetInScope 内）。
	store := &fakeFlowScope{viewFlow: FlowRecord{ID: 5, Method: "GET"}, viewOK: false}
	vf, err := BuildViewFlow(store)
	if err != nil {
		t.Fatal(err)
	}
	it := vf.(tool.InvokableTool)
	if _, err := it.InvokableRun(context.Background(), `{"id":5}`); err == nil || !strings.Contains(err.Error(), "当前 task") {
		t.Fatalf("跨 task view 应被拒，得到: %v", err)
	}
}

func TestViewFlow_ReturnsFull(t *testing.T) {
	store := &fakeFlowScope{viewFlow: FlowRecord{
		ID: 5, Method: "POST", URL: "http://t/login",
		RequestBody: []byte("user=admin"), StatusCode: 200,
	}, viewOK: true}
	vf, _ := BuildViewFlow(store)
	out := invoke(t, vf, `{"id":5}`)
	if !strings.Contains(out, "user=admin") || !strings.Contains(out, "/login") {
		t.Fatalf("view 应返回完整请求: %s", out)
	}
}
