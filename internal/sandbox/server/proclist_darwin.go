//go:build darwin

package server

import (
	"bytes"
	"encoding/binary"
	"errors"

	"golang.org/x/sys/unix"
)

// listProcesses 列出全部进程号（sysctl kern.proc.all）。
func listProcesses() ([]int, error) {
	procs, err := unix.SysctlKinfoProcSlice("kern.proc.all")
	if err != nil {
		return nil, err
	}
	pids := make([]int, 0, len(procs))
	for i := range procs {
		pids = append(pids, int(procs[i].Proc.P_pid))
	}
	return pids, nil
}

// processEnv 读取进程的环境块（\0 分隔）。sysctl kern.procargs2 返回参数个数、可执行文件路径、
// 各个参数，之后才是环境变量；只取环境部分——参数里出现的同形字符串不算归属。只能读同一
// 用户的进程，与沙盒能结束的进程范围一致。
func processEnv(pid int) ([]byte, error) {
	raw, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil {
		return nil, err
	}
	if len(raw) < 4 {
		return nil, errors.New("procargs2 过短")
	}
	argc := int(binary.LittleEndian.Uint32(raw[:4]))
	rest := raw[4:]
	// 可执行文件路径，及其后的对齐填充 \0。
	i := bytes.IndexByte(rest, 0)
	if i < 0 {
		return nil, errors.New("procargs2 缺可执行文件路径")
	}
	rest = bytes.TrimLeft(rest[i:], "\x00")
	for ; argc > 0; argc-- {
		i = bytes.IndexByte(rest, 0)
		if i < 0 {
			return nil, errors.New("procargs2 参数不完整")
		}
		rest = rest[i+1:]
	}
	// 环境变量以空串结束，其后是系统附加的键值，不属于环境。
	if end := bytes.Index(rest, []byte{0, 0}); end >= 0 {
		rest = rest[:end+1]
	}
	return rest, nil
}
