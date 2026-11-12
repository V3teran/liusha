//go:build darwin

package server

import (
	"errors"
	"os/exec"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// taskProcesses 登记各任务的进程组（macOS 没有 /proc，无法按任务标识识别进程）。组长退出后
// 组内仍有进程（后台监听器等）则继续保留登记，任务释放时整组结束；组空即注销。macOS 只作
// 开发机：沙盒重启后上一实例的进程不再被认领。
type taskProcesses struct {
	mu     sync.Mutex
	byTask map[string]map[int]struct{}
}

func newTaskProcesses() *taskProcesses {
	return &taskProcesses{byTask: map[string]map[int]struct{}{}}
}

func (p *taskProcesses) start(taskID string, cmd *exec.Cmd) (*procGroup, error) {
	g, err := startGroup(cmd)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	groups := p.byTask[taskID]
	if groups == nil {
		groups = map[int]struct{}{}
		p.byTask[taskID] = groups
	}
	groups[g.pgid] = struct{}{}
	return g, nil
}

// finish 在组长退出后调用：组空即注销。检查与注销在同一把锁内：组号被复用、又被同一任务新的
// 进程组登记时，不会误删新记录。
func (p *taskProcesses) finish(taskID string, g *procGroup) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if groupAlive(g.pgid) {
		return
	}
	delete(p.byTask[taskID], g.pgid)
	if len(p.byTask[taskID]) == 0 {
		delete(p.byTask, taskID)
	}
}

// killTask 结束任务的全部登记进程组，返回结束的进程数。
func (p *taskProcesses) killTask(taskID string) int {
	p.mu.Lock()
	groups := p.byTask[taskID]
	delete(p.byTask, taskID)
	p.mu.Unlock()

	ended := 0
	for pgid := range groups {
		procs, _ := unix.SysctlKinfoProcSlice("kern.proc.pgrp", pgid)
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			ended += len(procs)
		}
	}
	return ended
}

// killLeftover：macOS 上不认领上一沙盒实例的进程（无法核验归属）。
func (*taskProcesses) killLeftover(string, int) bool { return false }

// groupAlive 报告进程组内是否还有进程（信号 0 只做存在性检查）。
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
