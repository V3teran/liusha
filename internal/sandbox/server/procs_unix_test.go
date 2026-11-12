//go:build !windows

package server

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// backgroundSleepPID 让命令在后台留一个长命进程并输出它的 pid。
const backgroundSleepPID = `sleep 300 >/dev/null 2>&1 & echo $!`

// processAlive 报告进程是否仍在运行。已结束但未被回收的僵尸进程算作已结束：没有 init 回收
// 孤儿的环境（如无 --init 的容器）里，被结束的后台进程会以僵尸形态残留。
func processAlive(pid int) bool {
	if stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat")); err == nil {
		// 格式：pid (comm) state ...；comm 可含空格与括号，取最后一个 ')' 之后的状态位。
		rest := bytes.TrimSpace(stat[bytes.LastIndexByte(stat, ')')+1:])
		return len(rest) > 0 && rest[0] != 'Z'
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

func killProcess(pid int) { _ = syscall.Kill(pid, syscall.SIGKILL) }

// 进程组对象发布之前的取消也结束整组：组长此时已能派生子进程。
func TestCancelBeforeGroupPublished_EndsWholeGroup(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "child.pid")
	cmd := exec.Command("sh", "-c", `sleep 300 & echo $! > "$1"; wait`, "sh", pidFile) // #nosec G204 // 测试内固定命令
	if _, err := startGroup(cmd); err != nil {
		t.Fatal(err)
	}
	var child int
	for i := 0; i < 100 && child == 0; i++ {
		if b, err := os.ReadFile(pidFile); err == nil {
			child, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		}
		time.Sleep(20 * time.Millisecond)
	}
	if child == 0 {
		t.Fatal("子进程未启动")
	}
	t.Cleanup(func() { killProcess(child) })

	if err := cancelUnpublished(cmd); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	deadline := time.Now().Add(3 * time.Second)
	for processAlive(child) && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if processAlive(child) {
		t.Fatal("取消应结束组内的子进程")
	}
}
