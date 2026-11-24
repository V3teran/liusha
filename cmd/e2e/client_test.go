package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// fakeRun 是 fakeAPI 里任务当前存活 run 的状态。
type fakeRun struct {
	live bool      // 是否有存活的 run
	beat time.Time // 存活心跳时刻
	fail bool      // 读 run 的接口是否报错
}

// fakeAPI 模拟 api 的任务接口；run 是任务当前存活 run 的状态。
func fakeAPI(t *testing.T, run *fakeRun) taskAPI {
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
	mux.HandleFunc("GET /api/tasks/t1/run", func(w http.ResponseWriter, _ *http.Request) {
		if run.fail {
			http.Error(w, "boom", http.StatusInternalServerError)
			return
		}
		if !run.live {
			_, _ = w.Write([]byte(`{"run":null}`))
			return
		}
		_, _ = w.Write([]byte(`{"run":{"id":"r1","status":"running","heartbeat_at":"` + run.beat.UTC().Format(time.RFC3339Nano) + `"}}`))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return taskAPI{base: srv.URL, session: &http.Cookie{Name: sessionCookieName, Value: "s"}}
}

// e2e 经公开 API 观察任务：带会话读状态、按节点类型统计探索图、读当前存活 run 的心跳。
func TestTaskAPI_ObservesTaskThroughPublicAPI(t *testing.T) {
	beat := time.Now().Truncate(time.Second)
	api := fakeAPI(t, &fakeRun{live: true, beat: beat})
	ctx := context.Background()

	status, err := api.Status(ctx, "t1")
	if err != nil || status != "aborted" {
		t.Fatalf("status=%q err=%v", status, err)
	}
	stats, err := api.GraphStats(ctx, "t1")
	if err != nil || stats != (GraphStats{Objectives: 1, Actions: 2, Observations: 1, Results: 1}) {
		t.Fatalf("stats=%+v err=%v", stats, err)
	}
	run, live, err := api.LiveRun(ctx, "t1")
	if err != nil || !live || run.Status != "running" || !run.HeartbeatAt.Equal(beat) {
		t.Fatalf("run=%+v live=%v err=%v", run, live, err)
	}
	unauth := taskAPI{base: api.base, session: &http.Cookie{Name: sessionCookieName, Value: "x"}}
	if _, err := unauth.Status(ctx, "t1"); err == nil {
		t.Fatal("会话无效应报错")
	}
}

// 停滞只认当前 run 的存活心跳：心跳超过窗口没有续约才算停摆；没有存活的 run、读取失败都不判
// （宁可多等，不误判停滞）。
func TestRunIdle_OnlyWhenHeartbeatStops(t *testing.T) {
	ctx := context.Background()
	logger := zerolog.Nop()
	run := &fakeRun{live: true, beat: time.Now()}
	api := fakeAPI(t, run)

	if runIdle(ctx, api, "t1", &logger) {
		t.Fatal("心跳新鲜时不应判停滞")
	}
	run.beat = time.Now().Add(-heartbeatIdleWindow - time.Second)
	if !runIdle(ctx, api, "t1", &logger) {
		t.Fatal("心跳超过窗口没有续约应判停滞")
	}
	run.live = false
	if runIdle(ctx, api, "t1", &logger) {
		t.Fatal("没有存活的 run 时不应在这里判停滞")
	}
	run.live, run.fail = true, true
	if runIdle(ctx, api, "t1", &logger) {
		t.Fatal("读取失败应按有活动处理")
	}
}
