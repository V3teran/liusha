//go:build !windows

package server

import (
	"os/exec"
	"syscall"
)

// procGroup 是一个 POSIX 进程组（组号即组长 pid）。
type procGroup struct{ pgid int }

// kill 结束整组。
func (g *procGroup) kill() error { return syscall.Kill(-g.pgid, syscall.SIGKILL) }

// startGroup 在新进程组中启动 cmd：终端信号不波及它，结束时整组结束。
func startGroup(cmd *exec.Cmd) (*procGroup, error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &procGroup{pgid: cmd.Process.Pid}, nil
}

// cancelUnpublished 结束组对象发布之前的进程组：组号即组长 pid，组长此时可能已派生子进程，
// 按组结束。
func cancelUnpublished(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
