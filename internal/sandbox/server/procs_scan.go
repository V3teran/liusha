//go:build linux || darwin

package server

import (
	"bytes"
	"os"
	"os/exec"
	"syscall"
)

// taskProcesses 识别并结束任务的进程。进程的任务归属以环境变量 LIUSHA_TASK_ID（沙盒为每个
// 进程组注入，子孙继承）为准，在发信号那一刻从系统读取核验——不登记组号，因此不受组号被
// 系统复用的影响；脱离命令进程组的进程、沙盒重启前上一实例启动的进程同样识别得到。进程列表
// 与进程环境的读取方式按平台而定（listProcesses / processEnv）。
type taskProcesses struct{}

func newTaskProcesses() *taskProcesses { return &taskProcesses{} }

func (*taskProcesses) start(_ string, cmd *exec.Cmd) (*procGroup, error) { return startGroup(cmd) }

// finish 在组长退出后调用：任务归属不靠登记，无事可做。
func (*taskProcesses) finish(string, *procGroup) {}

// killTask 结束带有该任务标识的进程所在的全部进程组，返回结束的进程数。chromium 的子进程会
// 清理环境变量，但与主进程同组，结束整组即一并带走（计入结束数）。沙盒自身所在的进程组永不触碰。
func (*taskProcesses) killTask(taskID string) int {
	marker := []byte("LIUSHA_TASK_ID=" + taskID)
	self := syscall.Getpgrp()
	pids, err := listProcesses()
	if err != nil {
		releaseLog.Warn().Err(err).Msg("列出进程失败，无法结束任务进程")
		return 0
	}
	members := map[int][]int{} // 进程组 → 组内进程
	groups := map[int]bool{}   // 含任务进程的组
	for _, pid := range pids {
		if pid == os.Getpid() {
			continue
		}
		pgid, err := syscall.Getpgid(pid)
		if err != nil || pgid == self {
			continue // 进程已退出，或属于沙盒自身
		}
		members[pgid] = append(members[pgid], pid)
		if ownedBy(pid, marker) {
			groups[pgid] = true
		}
	}
	ended := 0
	for pgid := range groups {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			ended += len(members[pgid])
		}
	}
	return ended
}

// killLeftover 结束上一沙盒实例为该任务启动、仍在运行的进程组（组长 pid 记在浏览器端点里）。
// 先核验任务标识，组号已被复用时不动它。
func (*taskProcesses) killLeftover(taskID string, pid int) bool {
	if pid <= 0 || !ownedBy(pid, []byte("LIUSHA_TASK_ID="+taskID)) {
		return false
	}
	return syscall.Kill(-pid, syscall.SIGKILL) == nil
}

// ownedBy 报告进程的环境里是否有 marker 这一项（进程已退出或无权读取时为否）。
func ownedBy(pid int, marker []byte) bool {
	env, err := processEnv(pid)
	return err == nil && hasEntry(env, marker)
}

// hasEntry 报告 \0 分隔的环境块中是否有与 entry 完全相同的一项。
func hasEntry(env, entry []byte) bool {
	for _, kv := range bytes.Split(env, []byte{0}) {
		if bytes.Equal(kv, entry) {
			return true
		}
	}
	return false
}
