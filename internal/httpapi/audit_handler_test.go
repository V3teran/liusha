package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/audit"
)

// fakeAuditStore 记录收到的筛选，返回预置页。
type fakeAuditStore struct {
	got audit.Filter
	res audit.Page
	err error
}

func (f *fakeAuditStore) List(_ context.Context, f2 audit.Filter) (audit.Page, error) {
	f.got = f2
	return f.res, f.err
}

// TestListAudit_HappyPath：筛选参数逐项落到 Filter；分页换算为 offset；响应带词表与总数。
func TestListAudit_HappyPath(t *testing.T) {
	now := time.Now()
	fake := &fakeAuditStore{res: audit.Page{
		Items: []audit.Event{
			{ID: 7, Actor: "user:admin", Action: audit.ActionTaskCreate, TargetKind: "task", TargetID: "t1",
				Metadata: json.RawMessage(`{"source":"direct"}`), CreatedAt: now},
			{ID: 6, Actor: "runner", Action: audit.ActionTaskAbort, TargetKind: "task", TargetID: "t2",
				Metadata: json.RawMessage(`{}`), CreatedAt: now.Add(-time.Minute)},
		},
		Total: 12,
	}}
	srv := newTestServer(t, Deps{AuditReader: fake})
	defer srv.Close()

	code, body := doJSON(t, "GET",
		srv.URL+"/api/audit?actor=adm&action=task.create&target_kind=task&start=2026-01-01T00:00:00Z&page=3&size=25", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if fake.got.Actor != "adm" || fake.got.Action != "task.create" || fake.got.TargetKind != "task" {
		t.Fatalf("筛选未透传: %+v", fake.got)
	}
	if fake.got.Limit != 25 || fake.got.Offset != 50 {
		t.Fatalf("分页换算错误: limit=%d offset=%d", fake.got.Limit, fake.got.Offset)
	}
	if fake.got.Start.IsZero() {
		t.Fatal("start 未解析")
	}
	if body["total"] != float64(12) || body["page"] != float64(3) {
		t.Fatalf("total/page 错误: %v %v", body["total"], body["page"])
	}
	actions, _ := body["actions"].([]any)
	if len(actions) == 0 {
		t.Fatal("响应应带动作词表")
	}
	items, _ := body["items"].([]any)
	first := items[0].(map[string]any)
	if first["metadata"].(map[string]any)["source"] != "direct" {
		t.Fatalf("metadata 应解包为对象: %v", first)
	}
}

// TestListAudit_UnknownActionRejected：动作参数不在词表内 → 400（防拼错悄悄变空结果）。
func TestListAudit_UnknownActionRejected(t *testing.T) {
	srv := newTestServer(t, Deps{AuditReader: &fakeAuditStore{}})
	defer srv.Close()

	code, _ := doJSON(t, "GET", srv.URL+"/api/audit?action=nope", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}

// TestListAudit_BadTimeRejected：时间参数非法 → 400。
func TestListAudit_BadTimeRejected(t *testing.T) {
	srv := newTestServer(t, Deps{AuditReader: &fakeAuditStore{}})
	defer srv.Close()

	code, _ := doJSON(t, "GET", srv.URL+"/api/audit?start=not-a-time", nil)
	if code != http.StatusBadRequest {
		t.Fatalf("status=%d, want 400", code)
	}
}

// TestListAudit_StoreError：底层失败 → respondErr 收敛为 500。
func TestListAudit_StoreError(t *testing.T) {
	srv := newTestServer(t, Deps{AuditReader: &fakeAuditStore{err: errors.New("db down")}})
	defer srv.Close()

	code, _ := doJSON(t, "GET", srv.URL+"/api/audit", nil)
	if code != http.StatusInternalServerError {
		t.Fatalf("status=%d, want 500", code)
	}
}

// TestAuditRouteNotRegisteredWithoutDep：AuditReader 为 nil 时路由不注册（404）。
func TestAuditRouteNotRegisteredWithoutDep(t *testing.T) {
	srv := newTestServer(t, Deps{})
	defer srv.Close()

	code, _ := doJSON(t, "GET", srv.URL+"/api/audit", nil)
	if code != http.StatusNotFound {
		t.Fatalf("status=%d, want 404", code)
	}
}
