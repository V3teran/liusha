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

	gotListFilter finding.LedgerFilter
	gotStatFilter finding.LedgerFilter
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
	if fk.gotListFilter.TaskID != taskID || fk.gotListFilter.Source != "direct" {
		t.Fatalf("列表筛选未透传: %+v", fk.gotListFilter)
	}
	if fk.gotStatFilter.TaskID != taskID {
		t.Fatalf("统计筛选未透传: %+v", fk.gotStatFilter)
	}
}
