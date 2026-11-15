package httpapi

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/finding"
)

// fakeFindings 是 FindingsAPI 的桩，记录 ListAll/StatFiltered 收到的筛选。
type fakeFindings struct {
	rows []finding.LedgerRow
	stat finding.Stat

	gotListFilter  finding.LedgerFilter
	gotStatFilter  finding.LedgerFilter
	gotFacetFilter finding.LedgerFilter
	facets         []finding.TaskFacet
	gotGoalQuery   string
	gotFacetLimit  int
}

func (f *fakeFindings) TaskFacets(_ context.Context, fl finding.LedgerFilter, goalQuery string, limit int) ([]finding.TaskFacet, bool, error) {
	f.gotFacetFilter, f.gotGoalQuery, f.gotFacetLimit = fl, goalQuery, limit
	return f.facets, false, nil
}

func (f *fakeFindings) ListAll(_ context.Context, fl finding.LedgerFilter) ([]finding.LedgerRow, error) {
	f.gotListFilter = fl
	return f.rows, nil
}

func (f *fakeFindings) StatFiltered(_ context.Context, fl finding.LedgerFilter) (finding.Stat, error) {
	f.gotStatFilter = fl
	return f.stat, nil
}

func (f *fakeFindings) CountAll(_ context.Context, _ finding.LedgerFilter) (int, error) {
	return len(f.rows), nil
}

func (f *fakeFindings) UpdateTriage(_ context.Context, _, _, _, _ string) (finding.Finding, error) {
	return finding.Finding{}, nil
}

// TestFindingsRoutes_StatRegistered 回归：GET /findings/stat 必须注册。
// 此前 handler 已实现、前端台账页也在调用，但路由漏挂——线上 404 且
// 无任何测试覆盖（handler 函数本身不可达，unused lint 反而报它）。
func TestFindingsRoutes_StatRegistered(t *testing.T) {
	fk := &fakeFindings{stat: finding.Stat{
		Total:      3,
		ByStatus:   map[string]int{"open": 2, "confirmed": 1},
		BySeverity: map[string]int{"high": 1, "medium": 2},
	}}
	srv := newTestServer(t, Deps{Findings: fk})
	defer srv.Close()

	var body finding.Stat
	resp := doReq(t, "GET", srv.URL+"/api/findings/stat?severity=high", "")
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d（路由未注册?）", resp.StatusCode)
	}
	decode(t, resp, &body)
	if body.Total != 3 || body.ByStatus["open"] != 2 || body.BySeverity["medium"] != 2 {
		t.Fatalf("stat=%+v", body)
	}
	if fk.gotStatFilter.Severity != "high" {
		t.Fatalf("筛选未透传: %+v", fk.gotStatFilter)
	}
}

// 台账列表与统计同一筛选口径：task 参数透传给两者；非法任务 ID 400（不让畸形输入下沉成 500）。
func TestFindings_TaskFilter(t *testing.T) {
	const taskID = "44444444-4444-4444-8444-444444444444"
	fk := &fakeFindings{}
	srv := newTestServer(t, Deps{Findings: fk})
	defer srv.Close()

	for _, path := range []string{"/api/findings", "/api/findings/stat"} {
		resp := doReq(t, "GET", srv.URL+path+"?task="+taskID+"&source=direct", "")
		if resp.StatusCode != 200 {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
		if bad := doReq(t, "GET", srv.URL+path+"?task=not-a-uuid", ""); bad.StatusCode != 400 {
			t.Fatalf("%s 非法 task 应 400，得 %d", path, bad.StatusCode)
		}
	}
	doReq(t, "GET", srv.URL+"/api/findings?task="+taskID+"&source=direct&search=xss", "")
	if fk.gotListFilter.TaskID != taskID || fk.gotListFilter.Source != "direct" || fk.gotListFilter.Search != "xss" {
		t.Fatalf("列表筛选未透传: %+v", fk.gotListFilter)
	}
	if fk.gotStatFilter.TaskID != taskID {
		t.Fatalf("统计筛选未透传: %+v", fk.gotStatFilter)
	}
}

// 按对话限定（从对话跳来）：conversation 参数透传给列表与统计；非法对话 ID 400。
func TestFindings_ConversationFilter(t *testing.T) {
	const convID = "55555555-5555-4555-8555-555555555555"
	fk := &fakeFindings{}
	srv := newTestServer(t, Deps{Findings: fk})
	defer srv.Close()

	for _, path := range []string{"/api/findings", "/api/findings/stat"} {
		if resp := doReq(t, "GET", srv.URL+path+"?conversation="+convID, ""); resp.StatusCode != 200 {
			t.Fatalf("%s status=%d", path, resp.StatusCode)
		}
		if bad := doReq(t, "GET", srv.URL+path+"?conversation=x", ""); bad.StatusCode != 400 {
			t.Fatalf("%s 非法 conversation 应 400，得 %d", path, bad.StatusCode)
		}
	}
	doReq(t, "GET", srv.URL+"/api/findings?conversation="+convID, "")
	if fk.gotListFilter.ConversationID != convID || fk.gotStatFilter.ConversationID != convID {
		t.Fatalf("对话筛选未透传: list=%+v stat=%+v", fk.gotListFilter, fk.gotStatFilter)
	}
}

// 任务分面：与列表同一筛选参数；空结果是数组而不是 null。
func TestFindings_TaskFacets(t *testing.T) {
	fk := &fakeFindings{}
	srv := newTestServer(t, Deps{Findings: fk})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/findings/tasks?source=proxy", nil)
	if code != 200 {
		t.Fatalf("status=%d", code)
	}
	if tasks, ok := body["tasks"].([]any); !ok || len(tasks) != 0 {
		t.Fatalf("空结果应为空数组: %v", body)
	}
	if fk.gotFacetFilter.Source != "proxy" || fk.gotFacetLimit != 20 {
		t.Fatalf("筛选未透传或缺省上限不是 20: %+v %d", fk.gotFacetFilter, fk.gotFacetLimit)
	}
	if body["has_more"] != false {
		t.Fatalf("应带 has_more: %v", body)
	}

	// search 在任务资源上按目标搜索，不作为台账关键词；limit 钳到 50
	doJSON(t, "GET", srv.URL+"/api/findings/tasks?search=商城&limit=500", nil)
	if fk.gotGoalQuery != "商城" || fk.gotFacetFilter.Search != "" || fk.gotFacetLimit != 50 {
		t.Fatalf("目标搜索 / 上限不符: %q %+v %d", fk.gotGoalQuery, fk.gotFacetFilter, fk.gotFacetLimit)
	}

	fk.facets = []finding.TaskFacet{{TaskID: "t-1", Goal: "对 http://a 做测试", Count: 2}}
	_, body = doJSON(t, "GET", srv.URL+"/api/findings/tasks", nil)
	first := body["tasks"].([]any)[0].(map[string]any)
	if first["id"] != "t-1" || first["goal"] != "对 http://a 做测试" || first["count"] != float64(2) {
		t.Fatalf("字段不符: %v", first)
	}
	if code, _ := doJSON(t, "GET", srv.URL+"/api/findings/tasks?source=bogus", nil); code != 400 {
		t.Fatalf("非法来源应 400，得 %d", code)
	}
}
