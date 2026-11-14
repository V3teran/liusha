//go:build windows

package sandbox

import (
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/windows/registry"
)

// shellCandidates 列出 Git for Windows 的 sh.exe 可能的位置，按可信度排序：安装程序写的注册表
// （本机安装、当前用户安装），PATH 里 git 所在的安装根，各缺省安装目录。
func shellCandidates() []string {
	var out []string
	for _, root := range []registry.Key{registry.LOCAL_MACHINE, registry.CURRENT_USER} {
		if dir := installPath(root); dir != "" {
			out = append(out, filepath.Join(dir, "bin", "sh.exe"))
		}
	}
	if git, err := exec.LookPath("git"); err == nil {
		if sh := gitShellFromGitExe(git); sh != "" {
			out = append(out, sh)
		}
	}
	for _, env := range []string{"ProgramFiles", "ProgramW6432", "ProgramFiles(x86)"} {
		if dir := os.Getenv(env); dir != "" {
			out = append(out, filepath.Join(dir, "Git", "bin", "sh.exe"))
		}
	}
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		out = append(out, filepath.Join(dir, "Programs", "Git", "bin", "sh.exe"))
	}
	return out
}

// installPath 读取 Git for Windows 安装程序记下的安装目录。
func installPath(root registry.Key) string {
	k, err := registry.OpenKey(root, `SOFTWARE\GitForWindows`, registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer func() { _ = k.Close() }()
	dir, _, err := k.GetStringValue("InstallPath")
	if err != nil {
		return ""
	}
	return dir
}
