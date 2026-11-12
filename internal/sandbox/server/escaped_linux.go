//go:build linux

package server

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
)

// killEscaped 结束脱离了任务命令进程组、但仍属于该任务的进程，返回结束的进程组数。
//
// 浏览器服务以独立会话启动（自身成为新进程组），chromium 又在其下派生；它们不在登记的命令
// 进程组里，浏览器服务的 reset 也不总能带走启动到一半的 chromium。这些进程都继承了命令环境
// 中的 LIUSHA_TASK_ID，按它识别后结束其整个进程组（chromium 的子进程会清理环境变量，但与
// 主进程同组）。沙盒自身所在的进程组永不触碰。
func killEscaped(taskID string) int {
	marker := []byte("LIUSHA_TASK_ID=" + taskID)
	self := syscall.Getpgrp()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		releaseLog.Warn().Err(err).Msg("读取 /proc 失败，无法清理脱离进程组的任务进程")
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
