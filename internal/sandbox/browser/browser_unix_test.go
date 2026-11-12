//go:build !windows

package browser

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// kill 结束端点记录的服务所在的整个进程组（服务以新会话启动，浏览器与它同组），并删除端点。
func TestClient_KillEndsServiceGroup(t *testing.T) {
	python := testPython(t)
	dir := t.TempDir()
	if _, err := Install(dir, python); err != nil {
		t.Fatal(err)
	}
	endpoints := t.TempDir()
	svc := exec.Command("sh", "-c", "sleep 300 & exec sleep 300") // 组长 + 同组子进程，模拟服务与浏览器
	svc.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(-svc.Process.Pid, syscall.SIGKILL); _ = svc.Wait() })
	writeEndpointPid(t, endpoints, "task1-default", svc.Process.Pid)

	cmd := exec.Command(python, filepath.Join(dir, "client.py"), "kill") // #nosec G204 // 测试内释放的脚本
	cmd.Env = append(os.Environ(), "IDENTITY=task1-default", "LIUSHA_BROWSER_DIR="+endpoints)
	if out, err := cmd.CombinedOutput(); err != nil || string(out) != "ok\n" {
		t.Fatalf("kill: %q %v", out, err)
	}
	done := make(chan error, 1)
	go func() { done <- svc.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("服务进程应被信号结束，而不是正常退出")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("服务进程应被结束")
	}
	if err := syscall.Kill(-svc.Process.Pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("服务进程组应整组结束: %v", err)
	}
	if _, err := os.Stat(filepath.Join(endpoints, "task1-default.json")); !os.IsNotExist(err) {
		t.Fatal("端点文件应被删除")
	}
}

func writeEndpointPid(t *testing.T, dir, identity string, pid int) {
	t.Helper()
	body := []byte(`{"port": 1, "key": "k", "pid": ` + strconv.Itoa(pid) + `}`)
	if err := os.WriteFile(filepath.Join(dir, identity+".json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
}
