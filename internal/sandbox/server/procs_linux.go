//go:build linux

package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// taskProcesses 识别并结束任务的进程。进程的任务归属以命令环境注入、子孙进程继承的
// LIUSHA_TASK_ID 为准，在发信号那一刻从 /proc 读取核验——不登记进程组号，因此不受组号
// 被系统复用的影响；脱离命令进程组的进程（浏览器服务以独立会话启动，chromium 在其下派生）
// 同样识别得到。
type taskProcesses struct{}

func newTaskProcesses() *taskProcesses { return &taskProcesses{} }

// add / forgetIfGone：Linux 上任务归属不靠登记。
func (*taskProcesses) add(string, int)          {}
func (*taskProcesses) forgetIfGone(string, int) {}

// killTask 结束带有该任务标识的全部进程所在的进程组，返回结束的组数。chromium 的子进程会
// 清理环境变量，但与主进程同组，结束整组即一并带走。沙盒自身所在的进程组永不触碰。
func (*taskProcesses) killTask(taskID string) int {
	marker := []byte("LIUSHA_TASK_ID=" + taskID)
	self := syscall.Getpgrp()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		releaseLog.Warn().Err(err).Msg("读取 /proc 失败，无法结束任务进程")
		return 0
	}
	groups := map[int]bool{}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err != nil || !hasEntry(env, marker) {
			continue // 进程已退出或无权读取：都不是要清理的对象
		}
		pgid, err := syscall.Getpgid(pid)
		if err != nil || pgid == self {
			continue
		}
		groups[pgid] = true
	}
	killed := 0
	for pgid := range groups {
		if err := syscall.Kill(-pgid, syscall.SIGKILL); err == nil {
			killed++
		}
	}
	return killed
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
