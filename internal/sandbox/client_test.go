package sandbox

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestHTTPClient_Exec_Success：正常 200 响应解码到 ExecResult。
func TestHTTPClient_Exec_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/exec" {
			http.Error(w, "wrong route", http.StatusNotFound)
			return
		}
		var req ExecRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ExecResult{
			ExitCode: 0,
			Stdout:   "got: " + req.Command,
			Files:    []Attachment{{Name: "x.txt", B64: "aGVsbG8="}},
		})
	}))
	defer server.Close()

	c := newHTTPClient(server.URL)
	res, err := c.Exec(context.Background(), ExecRequest{
		Command:        "echo hello",
		TimeoutSeconds: 5,
		Tag:            "test",
	})
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit_code=%d, want 0", res.ExitCode)
	}
	if res.Stdout != "got: echo hello" {
		t.Errorf("stdout=%q, want 'got: echo hello'", res.Stdout)
	}
	if len(res.Files) != 1 || res.Files[0].Name != "x.txt" || res.Files[0].B64 != "aGVsbG8=" {
		t.Errorf("files=%+v", res.Files)
	}
}

// TestHTTPClient_Exec_ErrorStatus：非 200 响应包装为 err。
func TestHTTPClient_Exec_ErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad command", http.StatusBadRequest)
	}))
	defer server.Close()

	c := newHTTPClient(server.URL)
	_, err := c.Exec(context.Background(), ExecRequest{Command: "x", TimeoutSeconds: 1})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "400") || !strings.Contains(err.Error(), "bad command") {
		t.Errorf("err=%v, want contains '400' + 'bad command'", err)
	}
}

// TestHTTPClient_Exec_ContextCanceled：ctx 被外部取消时 HTTP 请求立即中断。
//
// handler 用 select + timeout 双兜底——单纯 <-r.Context().Done() 可能因为
// server 端检测 client 断开有延迟而 hang，影响 httptest.Server.Close()。
func TestHTTPClient_Exec_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(2 * time.Second):
		}
	}))
	defer server.Close()

	c := newHTTPClient(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Exec(ctx, ExecRequest{Command: "x", TimeoutSeconds: 30})
	if err == nil {
		t.Fatal("expected ctx canceled error, got nil")
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Errorf("Exec 没在 ctx 取消后及时返回，耗时 %v", elapsed)
	}
}

// TestHTTPClient_Exec_ConnectionRefused：unreachable baseURL 返回连接错误。
func TestHTTPClient_Exec_ConnectionRefused(t *testing.T) {
	c := newHTTPClient("http://127.0.0.1:1")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := c.Exec(ctx, ExecRequest{Command: "x", TimeoutSeconds: 5})
	if err == nil {
		t.Fatal("expected connection error, got nil")
	}
}

// TestHTTPClient_Close_NoOp：Close 是 no-op，多次调用不报错。
func TestHTTPClient_Close_NoOp(t *testing.T) {
	c := newHTTPClient("http://localhost:1234")
	if err := c.Close(); err != nil {
		t.Errorf("Close should be no-op, got %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("second Close should be no-op, got %v", err)
	}
}
