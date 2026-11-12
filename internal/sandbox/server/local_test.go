package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// liveServer 在真实端口上运行沙盒服务（含本机接口），返回它与停止函数。
func liveServer(t *testing.T, opts ...func(*Config)) (*harness, func() error) {
	t.Helper()
	cfg := Config{Token: testToken, WorkspaceDir: t.TempDir(), Shell: testShell}
	for _, o := range opts {
		o(&cfg)
	}
	addr := freeAddr(t)
	srv := New(cfg)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx, addr) }()
	stop := sync.OnceValue(func() error { cancel(); return <-done })
	t.Cleanup(func() { _ = stop() })
	h := &harness{srv: srv, ts: &httptest.Server{URL: "http://" + addr}, root: cfg.WorkspaceDir}
	waitHealthy(t, h)
	return h, stop
}

// 本机接口的地址与口令随命令环境下发；凭口令访问；/ingest 带沙盒令牌转发给 runner、回传
// runner 的状态码。沙盒令牌不出现在命令环境里。
func TestLocalAPI_IngestForwardsWithToken(t *testing.T) {
	got := make(chan string, 1)
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- r.Header.Get("Authorization") + " " + string(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(runner.Close)
	h, stop := liveServer(t, func(c *Config) { c.IngestURL = runner.URL })

	env := execEnv(t, h, `printf '%s\n%s\n' "$LIUSHA_SANDBOX_LOCAL_URL" "$LIUSHA_SANDBOX_LOCAL_KEY"; env`)
	lines := strings.SplitN(env, "\n", 3)
	localURL, localKey := lines[0], lines[1]
	if !strings.HasPrefix(localURL, "http://127.0.0.1:") || localKey == "" {
		t.Fatalf("命令环境应带本机接口地址与口令: %q", env)
	}
	if strings.Contains(env, testToken) {
		t.Fatal("沙盒令牌不得出现在命令环境里")
	}

	post := func(key string) int {
		req, _ := http.NewRequest(http.MethodPost, localURL+"/ingest", strings.NewReader(`{"agent_run_id":"run-1"}`))
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("wrong"); code != http.StatusUnauthorized {
		t.Fatalf("口令不对应 401, got %d", code)
	}
	if code := post(localKey); code != http.StatusAccepted {
		t.Fatalf("应回传 runner 的状态码, got %d", code)
	}
	if want := "Bearer " + testToken + ` {"agent_run_id":"run-1"}`; <-got != want {
		t.Fatal("转发请求应带沙盒令牌与原样的请求体")
	}

	if err := stop(); err != nil {
		t.Fatalf("停止应干净: %v", err)
	}
	if resp, err := http.Post(localURL+"/ingest", "application/json", nil); err == nil {
		_ = resp.Body.Close()
		t.Fatal("停止后本机接口不应再接收")
	}
}

// freeAddr 返回一个当前无人监听的回环地址。
func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func waitHealthy(t *testing.T, h *harness) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if resp, err := http.Get(h.ts.URL + "/healthz"); err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("沙盒服务未就绪")
}

func execEnv(t *testing.T, h *harness, command string) string {
	t.Helper()
	body, _ := json.Marshal(sandbox.ExecRequest{AssignmentID: "asg", TaskID: "task", AgentRunID: "run-1", Command: command, TimeoutSeconds: 10})
	var res sandbox.ExecResult
	h.decode(t, http.MethodPost, "/exec", bytes.NewReader(body), &res)
	return res.Stdout
}
