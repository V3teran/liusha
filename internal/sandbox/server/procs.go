package server

import (
	"os/exec"
	"sync/atomic"
)

// 沙盒启动的每个进程（一条命令、一个浏览器服务）连同它派生的子孙构成一个进程组，归属一个
// 任务。进程组可以整体结束：命令超时 / 请求断开时结束这条命令的组；浏览器服务失去响应时
// 结束它的组；任务释放时结束任务的全部组（taskProcesses.killTask）。组的实现按平台而定：
// POSIX 是进程组（setpgid），Windows 是嵌套在任务作业里的作业对象。
//
// 这是资源回收，不是隔离：同一沙盒里的进程以同一用户运行，可以操作别的任务的进程。隔离的
// 单位是沙盒本身。

// runGroup 在归属 taskID 的新进程组里运行 cmd 直到结束；取消时结束整组。
func (p *taskProcesses) runGroup(taskID string, cmd *exec.Cmd) error {
	var started atomic.Pointer[procGroup]
	cmd.Cancel = func() error {
		if g := started.Load(); g != nil {
			return g.kill()
		}
		return cancelUnpublished(cmd) // Start 已返回、组对象还没发布：按平台直接结束
	}
	g, err := p.start(taskID, cmd)
	if err != nil {
		return err
	}
	started.Store(g)
	err = cmd.Wait()
	p.finish(taskID, g)
	return err
}
