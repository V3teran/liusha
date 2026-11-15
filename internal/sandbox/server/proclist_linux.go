//go:build linux

package server

import (
	"os"
	"strconv"
)

// listProcesses 列出全部进程号（/proc 下的数字目录）。
func listProcesses() ([]int, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(entries))
	for _, e := range entries {
		if pid, err := strconv.Atoi(e.Name()); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids, nil
}

// processEnv 读取进程的环境块（\0 分隔）。
func processEnv(pid int) ([]byte, error) {
	return os.ReadFile("/proc/" + strconv.Itoa(pid) + "/environ") // #nosec G304 G703 // 路径由数字进程号拼成
}
