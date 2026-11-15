//go:build !windows

package server

import (
	"fmt"
	"os"
	"syscall"
)

// fileID 是文件的身份（设备号 + inode），在取得时即固定下来。
type fileID struct{ dev, ino uint64 }

// identify 读取 path 指向的文件的身份。
func identify(path string) (fileID, error) {
	info, err := os.Stat(path)
	if err != nil {
		return fileID{}, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileID{}, fmt.Errorf("%s: 无法读取文件身份", path)
	}
	// 各平台字段类型不同（如 macOS 的 Dev 是 int32）；只作相等比较，不参与运算。
	return fileID{dev: uint64(st.Dev), ino: uint64(st.Ino)}, nil //nolint:unconvert,gosec // 见上
}
