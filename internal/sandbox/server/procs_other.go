//go:build !linux

package server

import (
	"errors"
	"sync"
	"syscall"
)

// taskProcesses 记录各任务的命令进程组（非 Linux 平台没有 /proc，无法按任务标识识别进程）。
// 命令结束后组内仍有进程（后台监听器等）则继续保留记录，任务释放时整组结束；组空即注销。
// 只为开发机能跑通：沙盒的部署目标是 Linux，脱离命令进程组的进程（浏览器服务、chromium）
// 在这里只能靠浏览器 reset 收尾。
type taskProcesses struct {
	mu     sync.Mutex
	byTask map[string]map[int]struct{}
}

func newTaskProcesses() *taskProcesses {
	return &taskProcesses{byTask: map[string]map[int]struct{}{}}
}

func (p *taskProcesses) add(taskID string, pgid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	groups := p.byTask[taskID]
	if groups == nil {
		groups = map[int]struct{}{}
		p.byTask[taskID] = groups
	}
	groups[pgid] = struct{}{}
}

// forgetIfGone 在进程组已无进程时注销它。检查与注销在同一把锁内：组号被复用、又被同一任务
// 新的执行登记时，不会误删新记录。
func (p *taskProcesses) forgetIfGone(taskID string, pgid int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if groupAlive(pgid) {
		return
	}
	delete(p.byTask[taskID], pgid)
	if len(p.byTask[taskID]) == 0 {
		delete(p.byTask, taskID)
	}
}

// killTask 结束任务的全部登记进程组，返回实际结束的组数。
func (p *taskProcesses) killTask(taskID string) int {
	p.mu.Lock()
	groups := p.byTask[taskID]
	delete(p.byTask, taskID)
	p.mu.Unlock()

	killed := 0
	for pgid := range groups {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	return killed
}

// groupAlive 报告进程组内是否还有进程（信号 0 只做存在性检查）。
func groupAlive(pgid int) bool {
	err := syscall.Kill(-pgid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}
