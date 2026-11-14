//go:build darwin

package server

import (
	"bytes"
	"os"
	"os/exec"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// 归属只看环境变量：参数里出现同形字符串不算，环境里有才算。
func TestProcessEnv_OnlyEnvironmentCounts(t *testing.T) {
	start := func(env []string) *exec.Cmd {
		cmd := exec.Command("/bin/sh", "-c", "sleep 30; true", "LIUSHA_TASK_ID=spoof") // 同形字符串作为参数（$0），sh 不被替换
		cmd.Env = env
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		time.Sleep(100 * time.Millisecond)
		return cmd
	}
	spoof := start(append(os.Environ(), "OTHER=1"))
	owned := start(append(os.Environ(), "LIUSHA_TASK_ID=real"))

	if raw, err := unix.SysctlRaw("kern.procargs2", spoof.Process.Pid); err != nil || !bytes.Contains(raw, []byte("LIUSHA_TASK_ID=spoof")) {
		t.Fatalf("前提：参数块里应含同形字符串（%v）", err)
	}
	if ownedBy(spoof.Process.Pid, []byte("LIUSHA_TASK_ID=spoof")) {
		t.Fatal("参数里的同形字符串不应算作归属")
	}
	if !ownedBy(owned.Process.Pid, []byte("LIUSHA_TASK_ID=real")) {
		t.Fatal("环境里的任务标识应被识别")
	}
}
