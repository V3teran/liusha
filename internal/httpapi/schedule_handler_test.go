package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/V3teran/liusha/internal/assignment"
	"github.com/V3teran/liusha/internal/cronschedule"
)

// fakeSchedules 是 SchedulesAPI 的内存实现：不存在的 id 与真实 store 同口径包装 pgx.ErrNoRows。
type fakeSchedules struct {
	rows    []cronschedule.Schedule
	deleted []string
	toggled map[string]bool
}

func (f *fakeSchedules) List(context.Context) ([]cronschedule.Schedule, error) { return f.rows, nil }

func (f *fakeSchedules) SetEnabled(_ context.Context, id string, enabled bool) (cronschedule.Schedule, error) {
	for _, r := range f.rows {
		if r.ID == id {
			if f.toggled == nil {
				f.toggled = map[string]bool{}
			}
			f.toggled[id] = enabled
			r.Enabled = enabled
			return r, nil
		}
	}
	return cronschedule.Schedule{}, fmt.Errorf("set cron_schedule %s: %w", id, pgx.ErrNoRows)
}

func (f *fakeSchedules) Delete(_ context.Context, id string) error {
	for _, r := range f.rows {
		if r.ID == id {
			f.deleted = append(f.deleted, id)
			return nil
		}
	}
	return fmt.Errorf("delete cron_schedule %s: %w", id, pgx.ErrNoRows)
}

const schedID = "11111111-1111-1111-1111-111111111111"

func newFakeSchedules() *fakeSchedules {
	next := time.Date(2026, 11, 12, 4, 0, 0, 0, time.UTC)
	payload, _ := json.Marshal([]assignment.Item{{Brief: "夜间复扫 http://a"}, {Brief: "夜间复扫 http://b"}})
	return &fakeSchedules{rows: []cronschedule.Schedule{{
		ID: schedID, CronExpr: "0 4 * * *", Payload: payload, Title: "夜间复扫",
		Enabled: true, NextRunAt: &next, CreatedAt: next.Add(-24 * time.Hour),
	}}}
}

// TestListSchedules：列出模板，briefs 由 payload 展开，cron/启用态/下次触发如实下发。
func TestListSchedules(t *testing.T) {
	srv := newTestServer(t, Deps{Schedules: newFakeSchedules()})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/schedules", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d body=%v", code, body)
	}
	items, _ := body["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("应返回 1 个模板: %v", body)
	}
	it := items[0].(map[string]any)
	if it["id"] != schedID || it["cron"] != "0 4 * * *" || it["enabled"] != true || it["title"] != "夜间复扫" {
		t.Fatalf("字段不符: %v", it)
	}
	briefs, _ := it["briefs"].([]any)
	if len(briefs) != 2 || briefs[0] != "夜间复扫 http://a" {
		t.Fatalf("briefs 应由 payload 展开: %v", it["briefs"])
	}
	if it["next_run_at"] == nil || it["last_run_at"] != nil {
		t.Fatalf("next_run_at 应有值、last_run_at 应为 null: %v", it)
	}
}

// TestListSchedules_EmptyIsArray：无模板时返回空数组而非 null。
func TestListSchedules_EmptyIsArray(t *testing.T) {
	srv := newTestServer(t, Deps{Schedules: &fakeSchedules{}})
	defer srv.Close()

	code, body := doJSON(t, "GET", srv.URL+"/api/schedules", nil)
	if code != http.StatusOK {
		t.Fatalf("status=%d", code)
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 0 {
		t.Fatalf("应为空数组: %v", body)
	}
}

// TestPatchSchedule：启停开关的契约——缺 enabled / 非法 UUID → 400，不存在 → 404，成功返回写后的模板。
func TestPatchSchedule(t *testing.T) {
	fs := newFakeSchedules()
	srv := newTestServer(t, Deps{Schedules: fs})
	defer srv.Close()

	cases := []struct {
		name string
		path string
		body any
		want int
	}{
		{"缺 enabled", "/api/schedules/" + schedID, map[string]any{}, http.StatusBadRequest},
		{"enabled 类型错误", "/api/schedules/" + schedID, map[string]any{"enabled": "no"}, http.StatusBadRequest},
		{"非法 UUID", "/api/schedules/not-a-uuid", map[string]any{"enabled": false}, http.StatusBadRequest},
		{"不存在", "/api/schedules/00000000-0000-0000-0000-000000000000", map[string]any{"enabled": false}, http.StatusNotFound},
		{"停用", "/api/schedules/" + schedID, map[string]any{"enabled": false}, http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, body := doJSON(t, "PATCH", srv.URL+tc.path, tc.body)
			if code != tc.want {
				t.Fatalf("want %d got %d (%v)", tc.want, code, body)
			}
		})
	}
	if v, ok := fs.toggled[schedID]; !ok || v {
		t.Fatalf("应以 enabled=false 调用 store，得 %v", fs.toggled)
	}
	_, body := doJSON(t, "PATCH", srv.URL+"/api/schedules/"+schedID, map[string]any{"enabled": false})
	if sc, _ := body["schedule"].(map[string]any); sc == nil || sc["enabled"] != false {
		t.Fatalf("应返回写后的模板: %v", body)
	}
}

// TestDeleteSchedule：删除成功 200；不存在 404；非法 UUID 400。
func TestDeleteSchedule(t *testing.T) {
	fs := newFakeSchedules()
	srv := newTestServer(t, Deps{Schedules: fs})
	defer srv.Close()

	if code, body := doJSON(t, "DELETE", srv.URL+"/api/schedules/"+schedID, nil); code != http.StatusOK {
		t.Fatalf("删除应 200，得 %d (%v)", code, body)
	}
	if len(fs.deleted) != 1 || fs.deleted[0] != schedID {
		t.Fatalf("未调用删除: %v", fs.deleted)
	}
	if code, _ := doJSON(t, "DELETE", srv.URL+"/api/schedules/00000000-0000-0000-0000-000000000000", nil); code != http.StatusNotFound {
		t.Fatalf("不存在应 404，得 %d", code)
	}
	if code, _ := doJSON(t, "DELETE", srv.URL+"/api/schedules/xyz", nil); code != http.StatusBadRequest {
		t.Fatalf("非法 UUID 应 400，得 %d", code)
	}
}
