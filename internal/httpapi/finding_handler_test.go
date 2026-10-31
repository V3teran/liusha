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

	gotStatFilter finding.LedgerFilter
}

func (f *fakeFindings) ListAll(_ context.Context, _ finding.LedgerFilter) ([]finding.LedgerRow, error) {
	return f.rows, nil
}

func (f *fakeFindings) StatFiltered(_ context.Context, fl finding.LedgerFilter) (finding.Stat, error) {
	f.gotStatFilter = fl
	return f.stat, nil
}

func (f *fakeFindings) CountAll(_ context.Context, _ finding.LedgerFilter) (int, error) {
	return len(f.rows), nil
}

func (f *fakeFindings) UpdateTriage(_ context.Context, _, _, _, _ string) (finding.VulnFinding, error) {
	return finding.VulnFinding{}, nil
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
