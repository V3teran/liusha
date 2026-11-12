//go:build linux

package server

import (
	"net/http"
	"strconv"
	"strings"
	"syscall"
	"testing"

	"github.com/V3teran/liusha/internal/sandbox"
)

// 以新会话脱离命令进程组的任务进程（浏览器服务即如此启动）也随任务释放结束；其他任务的不受影响。
func TestRelease_KillsEscapedTaskProcesses(t *testing.T) {
	h := newHarness(t)
	spawn := func(taskID string) int {
		res := h.execTask(t, taskID, `setsid sh -c 'echo $$ > escaped.pid; exec sleep 300' >/dev/null 2>&1 & sleep 0.3; cat escaped.pid`)
		pid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
		if err != nil {
			t.Fatalf("解析脱离进程 pid: %q %q", res.Stdout, res.Stderr)
		}
		return pid
	}
	pidA := spawn("task-a")
	pidB := spawn("task-b")
	t.Cleanup(func() { _ = syscall.Kill(pidA, syscall.SIGKILL); _ = syscall.Kill(pidB, syscall.SIGKILL) })

	var res sandbox.ReleaseResult
	h.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &res)
	if res.Escaped != 1 {
		t.Fatalf("应结束 1 个脱离的进程组, got %+v", res)
	}
	if !waitGone(t, pidA) {
		t.Fatal("task-a 脱离进程组的进程应被结束")
	}
	if !processAlive(pidB) {
		t.Fatal("task-b 的进程不应受影响")
	}
}
