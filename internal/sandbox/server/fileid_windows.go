//go:build windows

package server

import (
	"fmt"

	"golang.org/x/sys/windows"
)

// fileID 是文件的身份（卷序列号 + 文件索引），在取得时即固定下来。不用 os.FileInfo 比较：
// Windows 上它的身份是在比较时才按路径读取的，路径上的文件期间被替换就会认错。
type fileID struct {
	volume    uint32
	indexHigh uint32
	indexLow  uint32
}

// identify 读取 path 指向的文件的身份。
func identify(path string) (fileID, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return fileID{}, err
	}
	h, err := windows.CreateFile(p, 0, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		return fileID{}, fmt.Errorf("打开 %s: %w", path, err)
	}
	defer func() { _ = windows.CloseHandle(h) }()
	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return fileID{}, fmt.Errorf("读取 %s 的文件身份: %w", path, err)
	}
	return fileID{volume: info.VolumeSerialNumber, indexHigh: info.FileIndexHigh, indexLow: info.FileIndexLow}, nil
}
