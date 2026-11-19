package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestBenchClient_ParsesBusinessError 验证非 2xx 的统一错误体被解析为 benchError（带 code）。
func TestBenchClient_ParsesBusinessError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"invalid_state","message":"max active challenges reached","detail":{}}`))
	}))
	defer srv.Close()

	c := newBenchClient(srv.URL, "tok", nil)
	_, err := c.start(t.Context(), "web_01")
	var be *benchError
	if !errors.As(err, &be) {
		t.Fatalf("期望 benchError，得到 %v", err)
	}
	if be.Code != codeInvalidState {
		t.Fatalf("code = %q, want %q", be.Code, codeInvalidState)
	}
	if !be.isMaxActive() {
		t.Fatalf("message %q 应判为活跃题达上限", be.Message)
	}
	if be.taskEnded() {
		t.Fatalf("活跃满不应判为任务结束")
	}
}

// TestBenchError_TaskEnded 验证不含 max-active 语义的 invalid_state 判为任务结束。
func TestBenchError_TaskEnded(t *testing.T) {
	be := &benchError{Code: codeInvalidState, Message: "任务已结束（超时）", HTTPStatus: http.StatusConflict}
	if be.isMaxActive() {
		t.Fatal("不含上限语义不应判为活跃满")
	}
	if !be.taskEnded() {
		t.Fatal("应判为任务结束")
	}
}

// TestBenchError_TokenInvalid 验证 404 task_not_found 判为 token 失效（终止条件）。
func TestBenchError_TokenInvalid(t *testing.T) {
	be := &benchError{Code: codeTaskNotFound, HTTPStatus: http.StatusNotFound}
	if !be.tokenInvalid() {
		t.Fatal("task_not_found 应判为 token 失效")
	}
}

// TestBenchClient_SubmitSuccess 验证 submit 正常响应被解码。
func TestBenchClient_SubmitSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("BENCHMARK_TOKEN"); got != "tok" {
			t.Errorf("缺少 BENCHMARK_TOKEN 头，得到 %q", got)
		}
		_, _ = w.Write([]byte(`{"correct":true,"awarded":50,"cumulative_score":50,"correct_flag_count":1,"total_flag_count":2,"matched_flag_index":0}`))
	}))
	defer srv.Close()

	c := newBenchClient(srv.URL, "tok", nil)
	resp, err := c.submit(t.Context(), "web_01", "flag{x}")
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if !resp.Correct || resp.Awarded != 50 {
		t.Fatalf("resp = %+v", resp)
	}
}

// TestBenchClient_Duplicate 验证重复提交返回 duplicate 被识别。
func TestBenchClient_Duplicate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"code":"duplicate","message":"already submitted","detail":{}}`))
	}))
	defer srv.Close()

	c := newBenchClient(srv.URL, "tok", nil)
	_, err := c.submit(t.Context(), "web_01", "flag{x}")
	var be *benchError
	if !errors.As(err, &be) || be.Code != codeDuplicate {
		t.Fatalf("期望 duplicate，得到 %v", err)
	}
}

// TestBenchClient_DoesNotFollowRedirects 验证令牌不会随重定向带到别的源：自定义头
// BENCHMARK_TOKEN 不在标准库跨源剥离的范围内，客户端必须不跟随重定向。
func TestBenchClient_DoesNotFollowRedirects(t *testing.T) {
	var leaked string
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Get("BENCHMARK_TOKEN")
		_, _ = w.Write([]byte(`[]`))
	}))
	t.Cleanup(other.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	if _, err := newBenchClient(srv.URL, "tok", nil).listChallenges(t.Context()); err == nil {
		t.Fatal("重定向应按非 2xx 报错")
	}
	if leaked != "" {
		t.Fatalf("令牌被带到重定向目标: %q", leaked)
	}
}
