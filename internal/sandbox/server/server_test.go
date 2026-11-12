package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

const testToken = "test-token"

// harness 是一个跑在 httptest 上的沙盒服务，工作区在临时目录。
type harness struct {
	srv  *Server
	ts   *httptest.Server
	root string
}

func newHarness(t *testing.T, opts ...func(*Config)) *harness {
	t.Helper()
	cfg := Config{Token: testToken, WorkspaceDir: t.TempDir()}
	for _, o := range opts {
		o(&cfg)
	}
	srv := New(cfg)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return &harness{srv: srv, ts: ts, root: cfg.WorkspaceDir}
}

// do 发带令牌的请求。
func (h *harness) do(method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, h.ts.URL+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+testToken)
	return http.DefaultClient.Do(req)
}

func (h *harness) decode(t *testing.T, method, path string, body io.Reader, out any) {
	t.Helper()
	resp, err := h.do(method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("%s %s: status %d: %s", method, path, resp.StatusCode, b)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

// execTask 在指定任务下执行命令。
func (h *harness) execTask(t *testing.T, taskID, command string) sandbox.ExecResult {
	t.Helper()
	body, _ := json.Marshal(sandbox.ExecRequest{AssignmentID: "asg", TaskID: taskID, AgentID: "a", Command: command, TimeoutSeconds: 10})
	var res sandbox.ExecResult
	h.decode(t, http.MethodPost, "/exec", bytes.NewReader(body), &res)
	return res
}

func (h *harness) exec(t *testing.T, command string) sandbox.ExecResult {
	t.Helper()
	return h.execTask(t, "task", command)
}

// 除 /healthz 外都要令牌：缺失或错误一律 401。
func TestAuth_RequiresToken(t *testing.T) {
	h := newHarness(t)
	if resp, err := http.Get(h.ts.URL + "/healthz"); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz 无需令牌: %v %v", resp, err)
	}
	for _, auth := range []string{"", "Bearer wrong", "Bearer " + testToken + "x", testToken} {
		for _, ep := range []struct{ method, path string }{
			{http.MethodPost, "/exec"}, {http.MethodGet, "/tools"}, {http.MethodPost, "/tasks/t1/release"},
		} {
			req, _ := http.NewRequest(ep.method, h.ts.URL+ep.path, strings.NewReader("{}"))
			if auth != "" {
				req.Header.Set("Authorization", auth)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			_ = resp.Body.Close()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("%s %s Authorization=%q: status %d, 期望 401", ep.method, ep.path, auth, resp.StatusCode)
			}
		}
	}
}

// 未配置令牌的服务拒绝一切调用（fail-closed）。
func TestAuth_EmptyTokenRejectsAll(t *testing.T) {
	h := newHarness(t, func(c *Config) { c.Token = "" })
	req, _ := http.NewRequest(http.MethodGet, h.ts.URL+"/tools", nil)
	req.Header.Set("Authorization", "Bearer ")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status %d, 期望 401", resp.StatusCode)
	}
}

func processAlive(pid int) bool {
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func waitGone(t *testing.T, pid int) bool {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !processAlive(pid) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}

// 释放只结束本任务的进程：两个任务各留一个后台进程，释放 A 后 A 的进程消失、B 的仍在。
func TestRelease_KillsOnlyOwnTaskProcesses(t *testing.T) {
	h := newHarness(t)
	spawn := func(taskID string) int {
		res := h.execTask(t, taskID, `sleep 300 >/dev/null 2>&1 & echo $!`)
		pid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
		if err != nil {
			t.Fatalf("解析后台进程 pid: %q", res.Stdout)
		}
		return pid
	}
	pidA, pidB := spawn("task-a"), spawn("task-b")
	t.Cleanup(func() { _ = syscall.Kill(pidA, syscall.SIGKILL); _ = syscall.Kill(pidB, syscall.SIGKILL) })
	if !processAlive(pidA) || !processAlive(pidB) {
		t.Fatal("命令结束后后台进程应保留到任务释放")
	}

	var res sandbox.ReleaseResult
	h.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &res)
	if res.ProcessGroups != 1 {
		t.Fatalf("应结束 task-a 的 1 个进程组, got %+v", res)
	}
	if !waitGone(t, pidA) {
		t.Fatal("task-a 的后台进程应被结束")
	}
	if !processAlive(pidB) {
		t.Fatal("task-b 的进程不应受影响")
	}

	// 再次释放是空操作。
	h.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &res)
	if res.ProcessGroups != 0 {
		t.Fatalf("重复释放应为空操作, got %+v", res)
	}
	h.decode(t, http.MethodPost, "/tasks/task-b/release", nil, &res)
	if !waitGone(t, pidB) {
		t.Fatal("task-b 释放后其进程应被结束")
	}
}

// 没有遗留后台进程的命令结束即注销，释放时不会去动已复用的进程组号。
func TestRelease_ForgetsFinishedGroups(t *testing.T) {
	h := newHarness(t)
	h.execTask(t, "task-a", "true")
	var res sandbox.ReleaseResult
	h.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &res)
	if res.ProcessGroups != 0 {
		t.Fatalf("已结束的进程组不应再被记录, got %+v", res)
	}
}

func TestRelease_RejectsUnsafeTaskID(t *testing.T) {
	h := newHarness(t)
	resp, err := h.do(http.MethodPost, "/tasks/..%2Fetc/release", nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest && resp.StatusCode != http.StatusNotFound {
		t.Fatalf("非法任务 ID 应被拒绝, status %d", resp.StatusCode)
	}
}

// /tools 按工具清单探测 PATH 中是否可执行；浏览器能力单列，缺失时说明原因。
func TestTools_ProbesPath(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "fake-tool"), []byte("#!/bin/sh\n"), 0o700); err != nil { // #nosec G306 // 测试用可执行脚本
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "not-executable"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	h := newHarness(t, func(c *Config) {
		c.Tools = []string{"fake-tool", "not-executable", "missing-tool"}
		c.BrowserPython = "no-such-python"
	})
	var inv sandbox.Inventory
	h.decode(t, http.MethodGet, "/tools", nil, &inv)
	want := map[string]bool{"fake-tool": true, "not-executable": false, "missing-tool": false}
	for name, installed := range want {
		if got, ok := inv.Tools[name]; !ok || got != installed {
			t.Errorf("%s: installed=%v(存在=%v), 期望 %v", name, got, ok, installed)
		}
	}
	if inv.Browser.Installed || !strings.Contains(inv.Browser.Detail, "no-such-python") {
		t.Errorf("浏览器能力应不可用并说明原因, got %+v", inv.Browser)
	}
}

// 命令环境：去掉 liusha 的密钥（LIUSHA_ 前缀与配置列出的变量），保留用户给工具的凭据，
// 注入任务变量、回灌地址与令牌、浏览器脚本目录。
func TestExec_CommandEnvironment(t *testing.T) {
	t.Setenv("LIUSHA_POSTGRES_DSN", "postgres://u:pw@db/liusha")
	t.Setenv("LIUSHA_LLM_KEY_SECRET", "deadbeef")
	t.Setenv("MAIN_API_KEY", "llm-key")
	t.Setenv("SHODAN_API_KEY", "tool-key")
	browserBin := t.TempDir()
	h := newHarness(t, func(c *Config) {
		c.SecretEnv = []string{"MAIN_API_KEY"}
		c.IngestURL = "http://runner:8590/internal/v1/traffic/ingest"
		c.BrowserBin = browserBin
		c.ViewportWidth, c.ViewportHeight = 1280, 800
	})
	out := h.exec(t, "env").Stdout
	for _, leaked := range []string{"postgres://u:pw@db", "deadbeef", "llm-key"} {
		if strings.Contains(out, leaked) {
			t.Errorf("命令环境泄露了 %q", leaked)
		}
	}
	for _, want := range []string{
		"SHODAN_API_KEY=tool-key",
		"LIUSHA_TASK_ID=task",
		"LIUSHA_INGEST_URL=http://runner:8590/internal/v1/traffic/ingest",
		"LIUSHA_INGEST_TOKEN=" + testToken,
		"LIUSHA_VIEWPORT_WIDTH=1280",
		"PATH=" + browserBin + string(os.PathListSeparator),
	} {
		if !strings.Contains(out, want) {
			t.Errorf("命令环境缺少 %q", want)
		}
	}
}
