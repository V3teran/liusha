package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// fakeAPI 模拟 api 的任务接口；calls 是用量接口返回的累计调用次数。
func fakeAPI(t *testing.T, calls *int) taskAPI {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/tasks/t1", func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie(sessionCookieName); err != nil || c.Value != "s" {
			http.Error(w, `{"error":"未登录"}`, http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"id":"t1","status":"aborted"}`))
	})
	mux.HandleFunc("GET /api/tasks/t1/exploration/graph", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"nodes":[{"kind":"objective"},{"kind":"action"},{"kind":"action"},{"kind":"observation"},{"kind":"result"}],"edges":[]}`))
	})
	mux.HandleFunc("GET /api/tasks/t1/usage", func(w http.ResponseWriter, _ *http.Request) {
		if *calls < 0 {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"llm_calls":` + strconv.Itoa(*calls) + `,"tool_calls":1}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return taskAPI{base: srv.URL, session: &http.Cookie{Name: sessionCookieName, Value: "s"}}
}

// e2e 经公开 API 观察任务：带会话读状态、按节点类型统计探索图、累计调用次数。
func TestTaskAPI_ObservesTaskThroughPublicAPI(t *testing.T) {
	calls := 3
	api := fakeAPI(t, &calls)
	ctx := context.Background()

	status, err := api.Status(ctx, "t1")
	if err != nil || status != "aborted" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	stats, err := api.GraphStats(ctx, "t1")
	if err != nil || stats != (GraphStats{Objectives: 1, Actions: 2, Observations: 1, Results: 1}) {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	n, err := api.ActivityCount(ctx, "t1")
	if err != nil || n != 4 {
		t.Fatalf("activity=%d err=%v", n, err)
	}
	unauth := taskAPI{base: api.base, session: &http.Cookie{Name: sessionCookieName, Value: "x"}}
	if _, err := unauth.Status(ctx, "t1"); err == nil {
		t.Fatal("会话无效应报错")
	}
}

// 调用次数在窗口内不增长才算没有活动；有增长即重新计时；读取失败按有活动处理（不误判停滞）。
func TestActivityWatch_IdleOnlyWhenCountStopsGrowing(t *testing.T) {
	calls := 3
	api := fakeAPI(t, &calls)
	ctx := context.Background()
	logger := zerolog.Nop()
	var w activityWatch

	if w.idle(ctx, api, "t1", &logger) {
		t.Fatal("首次读取不应判停滞")
	}
	w.changedAt = time.Now().Add(-activityIdleWindow - time.Second)
	if !w.idle(ctx, api, "t1", &logger) {
		t.Fatal("次数超过窗口未增长应判停滞")
	}
	calls = 5
	if w.idle(ctx, api, "t1", &logger) {
		t.Fatal("次数增长后应重新计时")
	}
	w.changedAt = time.Now().Add(-activityIdleWindow - time.Second)
	calls = -1
	if w.idle(ctx, api, "t1", &logger) {
		t.Fatal("读取失败应按有活动处理")
	}
}
