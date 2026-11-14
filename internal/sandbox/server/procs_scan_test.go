//go:build linux || darwin

package server

import (
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/sandbox"
)

// escapedSleepPID 让命令留下一个以新会话脱离命令进程组的长命进程（浏览器服务早先即如此
// 启动），输出它的 pid。用 python 调 setsid：macOS 没有 setsid 命令。
const escapedSleepPID = `python3 -c 'import os,sys,time
if os.fork() == 0:
    os.setsid(); print(os.getpid(), flush=True); sys.stdout.close(); os.close(1); time.sleep(300)' 2>/dev/null &
sleep 0.5`

func spawnEscaped(t *testing.T, h *harness, taskID string) int {
	t.Helper()
	res := h.execTask(t, taskID, escapedSleepPID)
	pid, err := strconv.Atoi(strings.TrimSpace(res.Stdout))
	if err != nil {
		t.Fatalf("解析脱离进程 pid: %q %q", res.Stdout, res.Stderr)
	}
	t.Cleanup(func() { killProcess(pid) })
	return pid
}

// 脱离命令进程组的任务进程也随任务释放结束；其他任务的不受影响。
func TestRelease_KillsEscapedTaskProcesses(t *testing.T) {
	h := newHarness(t)
	pidA, pidB := spawnEscaped(t, h, "task-a"), spawnEscaped(t, h, "task-b")

	var res sandbox.ReleaseResult
	h.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &res)
	if res.Processes < 1 {
		t.Fatalf("应结束脱离进程组的进程, got %+v", res)
	}
	if !waitGone(t, pidA) {
		t.Fatal("task-a 脱离进程组的进程应被结束")
	}
	if !processAlive(pidB) {
		t.Fatal("task-b 的进程不应受影响")
	}
}

// 沙盒重启后（新的沙盒实例没有任何登记），释放任务照样结束上一实例为它启动的进程。
func TestRelease_ReclaimsProcessesOfPreviousInstance(t *testing.T) {
	before := newHarness(t)
	pid := spawnEscaped(t, before, "task-a")
	plain := before.execTask(t, "task-a", backgroundSleepPID)
	plainPID, _ := strconv.Atoi(strings.TrimSpace(plain.Stdout))
	t.Cleanup(func() { killProcess(plainPID) })

	after := newHarness(t, func(c *Config) { c.WorkspaceDir = before.root })
	var res sandbox.ReleaseResult
	after.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &res)
	if !waitGone(t, pid) || !waitGone(t, plainPID) {
		t.Fatalf("新实例释放任务应结束上一实例启动的进程, got %+v", res)
	}
}
