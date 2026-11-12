package sandbox

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeSandbox 记录收到的请求并按路由回固定响应。
func fakeSandbox(t *testing.T, token string) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("POST /exec", func(w http.ResponseWriter, r *http.Request) {
		var req ExecRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(ExecResult{Stdout: "got: " + req.Command, Files: []Attachment{{Name: "x.txt", B64: "aGVsbG8="}}})
	})
	mux.HandleFunc("GET /tools", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(Inventory{Tools: map[string]bool{"nmap": true, "sqlmap": false}, Browser: BrowserStatus{Installed: true}})
	})
	mux.HandleFunc("POST /tasks/{id}/release", func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("id") != "task-1" {
			http.Error(w, "wrong task", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(ReleaseResult{ProcessGroups: 2, Browsers: 1})
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPClient_CallsWithToken(t *testing.T) {
	srv := fakeSandbox(t, "s3cret")
	c := NewClient(srv.URL+"/", "s3cret") // 末尾斜杠不应产生 //exec
	ctx := context.Background()

	res, err := c.Exec(ctx, ExecRequest{Command: "echo hello", TimeoutSeconds: 5})
	if err != nil || res.Stdout != "got: echo hello" || len(res.Files) != 1 {
		t.Fatalf("Exec: res=%+v err=%v", res, err)
	}
	inv, err := c.Inventory(ctx)
	if err != nil || !inv.Tools["nmap"] || inv.Tools["sqlmap"] || !inv.Browser.Installed {
		t.Fatalf("Inventory: inv=%+v err=%v", inv, err)
	}
	rel, err := c.Release(ctx, "task-1")
	if err != nil || rel.ProcessGroups != 2 || rel.Browsers != 1 {
		t.Fatalf("Release: res=%+v err=%v", rel, err)
	}
}

// 非 200 响应包装为带状态码与正文的错误（令牌错误即在这里暴露）。
func TestHTTPClient_ErrorStatus(t *testing.T) {
	srv := fakeSandbox(t, "s3cret")
	_, err := NewClient(srv.URL, "wrong").Inventory(context.Background())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("应报 401, got %v", err)
	}
}

// ctx 取消即中断请求，不等命令跑完。
func TestHTTPClient_ExecCanceled(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-block:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(block); srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := NewClient(srv.URL, "t").Exec(ctx, ExecRequest{Command: "sleep 60", TimeoutSeconds: 60})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("应因 ctx 超时中断, got %v", err)
	}
}

// 暂不可达（连不上、5xx）与拒绝请求（4xx）分开：前者随沙盒恢复而消失，后者是配置错误。
func TestUnavailable(t *testing.T) {
	srv := fakeSandbox(t, "s3cret")
	_, err := NewClient(srv.URL, "wrong").Inventory(context.Background())
	if err == nil || Unavailable(err) {
		t.Fatalf("令牌错误不是暂不可达: %v", err)
	}
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "busy", http.StatusBadGateway)
	}))
	t.Cleanup(down.Close)
	if _, err := NewClient(down.URL, "t").Inventory(context.Background()); !Unavailable(err) {
		t.Fatalf("5xx 应视为暂不可达: %v", err)
	}
	gone := httptest.NewServer(http.NotFoundHandler())
	url := gone.URL
	gone.Close()
	if _, err := NewClient(url, "t").Inventory(context.Background()); !Unavailable(err) {
		t.Fatalf("连不上应视为暂不可达: %v", err)
	}
	if Unavailable(nil) {
		t.Fatal("nil 不是错误")
	}
}
