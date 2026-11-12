package server

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 浏览器服务经本地套接字投递流量：沙盒带令牌转发给 runner，回传 runner 的状态码；套接字仅属主可连。
func TestIngestRelay_ForwardsWithToken(t *testing.T) {
	got := make(chan string, 1)
	runner := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		got <- r.Header.Get("Authorization") + " " + string(body)
		w.WriteHeader(http.StatusAccepted)
	}))
	t.Cleanup(runner.Close)

	dir, err := os.MkdirTemp("/tmp", "lsi") // unix 套接字路径有长度上限
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "ingest.sock")

	srv := New(Config{Token: testToken, WorkspaceDir: t.TempDir(), IngestURL: runner.URL, IngestSocket: sock})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.ListenAndServe(ctx, "127.0.0.1:0") }()
	stop := sync.OnceValue(func() error { cancel(); return <-done })
	t.Cleanup(func() { _ = stop() })

	var info os.FileInfo
	for i := 0; i < 50; i++ {
		if info, err = os.Stat(sock); err == nil && info.Mode().Perm() == 0o600 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("回灌套接字应以 0600 就绪: %v %v", info, err)
	}

	client := &http.Client{Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", sock)
		},
	}}
	resp, err := client.Post("http://sandbox/ingest", "application/json", strings.NewReader(`{"agent_id":"act-1"}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("应回传 runner 的状态码, got %d", resp.StatusCode)
	}
	if want := "Bearer " + testToken + ` {"agent_id":"act-1"}`; <-got != want {
		t.Fatal("转发请求应带令牌与原样的请求体")
	}

	if err := stop(); err != nil {
		t.Fatalf("停止应干净: %v", err)
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Fatalf("停止后应删除套接字: %v", err)
	}
}
