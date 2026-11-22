package httpapi

import "testing"

// 任务列表的筛选参数写错时 400，而不是悄悄不筛或返回空表。
func TestListTasks_InvalidFilterRejected(t *testing.T) {
	srv := newTestServer(t, Deps{Tasks: &fakeTaskSummaries{}})
	defer srv.Close()
	for _, q := range []string{"status=bogus", "since=yesterday", "until=2026-13-01"} {
		resp := doReq(t, "GET", srv.URL+"/api/tasks?"+q, "")
		_ = resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s 应 400，得 %d", q, resp.StatusCode)
		}
	}
}
