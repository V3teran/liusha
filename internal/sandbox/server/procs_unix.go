//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

// setProcessGroup 让命令在自己的进程组中运行：终端信号不波及它，取消（超时 / 请求断开）时
// 结束整组——命令派生的子孙进程一并结束，脱离进程组的（以新会话启动的浏览器服务）不受影响。
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
