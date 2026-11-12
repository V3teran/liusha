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

// 浏览器服务经回环中继投递流量：中继地址与口令随命令环境下发；凭口令接收、带沙盒令牌转发给
// runner、回传 runner 的状态码；口令不对一律拒绝。沙盒令牌不出现在命令环境里。
func TestIngestRelay_ForwardsWithToken(t *testing.T) {
	got := make(chan string, 1)
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- r.Header.Get("Authorization") + " " + string(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(runner.Close)

	addr := freeAddr(t)
	srv := New(Config{Token: testToken, WorkspaceDir: t.TempDir(), Shell: testShell, IngestURL: runner.URL})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx, addr) }()
	stop := sync.OnceValue(func() error { cancel(); return <-done })
	t.Cleanup(func() { _ = stop() })

	h := &harness{ts: &httptest.Server{URL: "http://" + addr}}
	waitHealthy(t, h)
	env := execEnv(t, h, `printf '%s\n%s\n' "$LIUSHA_INGEST_RELAY_URL" "$LIUSHA_INGEST_RELAY_KEY"; env`)
	lines := strings.SplitN(env, "\n", 3)
	relayURL, relayKey := lines[0], lines[1]
	if !strings.HasPrefix(relayURL, "http://127.0.0.1:") || relayKey == "" {
		t.Fatalf("命令环境应带回环中继地址与口令: %q", env)
	}
	if strings.Contains(env, testToken) {
		t.Fatal("沙盒令牌不得出现在命令环境里")
	}

	post := func(key string) int {
		req, _ := http.NewRequest(http.MethodPost, relayURL, strings.NewReader(`{"agent_run_id":"run-1"}`))
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
	if code := post(relayKey); code != http.StatusAccepted {
		t.Fatalf("应回传 runner 的状态码, got %d", code)
	}
	if want := "Bearer " + testToken + ` {"agent_run_id":"run-1"}`; <-got != want {
		t.Fatal("转发请求应带沙盒令牌与原样的请求体")
	}

	if err := stop(); err != nil {
		t.Fatalf("停止应干净: %v", err)
	}
	if code, err := http.Post(relayURL, "application/json", nil); err == nil {
		_ = code.Body.Close()
		t.Fatal("停止后中继不应再接收")
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
