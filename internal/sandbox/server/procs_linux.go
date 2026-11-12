//go:build linux

package server

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// taskProcesses 识别并结束任务的进程。进程的任务归属以命令环境注入、子孙进程继承的
// LIUSHA_TASK_ID 为准，在发信号那一刻从 /proc 读取核验——不登记进程组号，因此不受组号
// 被系统复用的影响；脱离命令进程组的进程（浏览器服务以独立会话启动，chromium 在其下派生）
// 同样识别得到。
//
// 这是资源回收，不是隔离：同一沙盒里的命令以同一用户运行，一个任务的命令可以改写自己的
// LIUSHA_TASK_ID、也可以直接向别的任务的进程发信号。任务之间互不隔离是沙盒的既定边界
// （隔离的单位是沙盒本身），这里只保证正常运行的任务结束时不留残余进程。
type taskProcesses struct{}

func newTaskProcesses() *taskProcesses { return &taskProcesses{} }

// run 在独立进程组中运行命令直到结束。任务归属随环境继承，无需登记。
func (*taskProcesses) run(_ string, cmd *exec.Cmd) error {
	setProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Wait()
}

// killTask 结束带有该任务标识的进程所在的全部进程组，返回结束的进程数。chromium 的子进程会
// 清理环境变量，但与主进程同组，结束整组即一并带走（计入结束数）。沙盒自身所在的进程组永不触碰。
func (*taskProcesses) killTask(taskID string) int {
	marker := []byte("LIUSHA_TASK_ID=" + taskID)
	self := syscall.Getpgrp()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		releaseLog.Warn().Err(err).Msg("读取 /proc 失败，无法结束任务进程")
		return 0
	}
	members := map[int][]int{} // 进程组 → 组内进程
	groups := map[int]bool{}   // 含任务进程的组
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		pgid, err := syscall.Getpgid(pid)
		if err != nil || pgid == self {
			continue // 进程已退出，或属于沙盒自身
		}
		members[pgid] = append(members[pgid], pid)
		env, err := os.ReadFile(filepath.Join("/proc", e.Name(), "environ"))
		if err == nil && hasEntry(env, marker) {
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

// hasEntry 报告 \0 分隔的环境块中是否有与 entry 完全相同的一项。
func hasEntry(env, entry []byte) bool {
	for _, kv := range bytes.Split(env, []byte{0}) {
		if bytes.Equal(kv, entry) {
			return true
		}
	}
	return false
}
