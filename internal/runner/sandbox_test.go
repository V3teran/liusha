package runner

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/V3teran/liusha/internal/sandbox"
	"github.com/V3teran/liusha/internal/sandbox/server"
)

// runner 经 HTTP 对接真实沙盒服务：工具留下的后台进程在 run 结束时随任务释放被结束。
func TestReleaseSandbox_EndsTaskProcesses(t *testing.T) {
	shell, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	srv := server.New(server.Config{Token: "tok", WorkspaceDir: t.TempDir(), Shell: shell})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	client := sandbox.NewClient(ts.URL, "tok")
	h := &Handler{Sandbox: client}

	res, err := client.Exec(context.Background(), sandbox.ExecRequest{
		AssignmentID: "asg", TaskID: "task-1", AgentRunID: "act-1",
		Command: `sleep 300 >/dev/null 2>&1 & echo $!`, TimeoutSeconds: 10,
	})
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatalf("解析 pid: %q", res.Stdout)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })

	h.releaseSandbox(zerolog.Nop(), "task-1")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if gone(pid) {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("run 结束释放任务后，后台进程应被结束")
}

// gone 报告进程已结束：不存在，或已结束但未被回收（无 init 回收孤儿的环境里残留为僵尸）。
func gone(pid int) bool {
	if stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); err == nil {
		rest := bytes.TrimSpace(stat[bytes.LastIndexByte(stat, ')')+1:])
		return len(rest) > 0 && rest[0] == 'Z'
	}
	return errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
