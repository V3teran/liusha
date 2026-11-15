package sandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// shellLookup 是查找 shell 用到的系统操作（测试可替换）。
type shellLookup struct {
	lookPath   func(string) (string, error)
	exists     func(string) bool
	candidates func() []string // 平台附加候选，按优先级；nil = 没有
}

// systemShellLookup 是真实系统上的查找。
func systemShellLookup() shellLookup {
	return shellLookup{
		lookPath: exec.LookPath,
		exists: func(path string) bool {
			info, err := os.Stat(path)
			return err == nil && !info.IsDir()
		},
		candidates: shellCandidates,
	}
}

// resolveShell 解析执行 agent 命令的 POSIX shell。configured 非空时只按它查找（名字按 PATH，
// 或绝对路径）；为空时自动查找：PATH 中的 sh，再依次试平台候选——Windows 没有自带的 sh，
// 候选是 Git for Windows 的 sh.exe（它的缺省安装不把 sh 加进 PATH）。
func resolveShell(configured string, l shellLookup) (string, error) {
	if configured != "" {
		path, err := l.lookPath(configured)
		if err != nil {
			return "", fmt.Errorf("找不到 sandbox.shell 指定的 shell %q: %w", configured, err)
		}
		return path, nil
	}
	if path, err := l.lookPath("sh"); err == nil {
		return path, nil
	}
	var tried []string
	if l.candidates != nil {
		for _, c := range l.candidates() {
			if l.exists(c) {
				return c, nil
			}
			tried = append(tried, c)
		}
	}
	msg := "找不到执行命令的 POSIX shell：PATH 中没有 sh"
	if len(tried) > 0 {
		msg += "，也不在 " + strings.Join(tried, "、")
	}
	return "", fmt.Errorf("%s。智能体的命令按 sh 语法执行：安装提供 sh 的环境（Windows 上如 Git for Windows），"+
		"或用 sandbox.shell（环境变量 LIUSHA_SANDBOX_SHELL）指定 sh 的路径", msg)
}

// gitShellFromGitExe 由 git 可执行文件的位置推出 Git for Windows 的 sh.exe：git 在安装根下的
// cmd\、bin\ 或 <工具链>\bin\（mingw64、clangarm64 等）里，sh 在 <根>\bin\sh.exe。推不出返回空。
func gitShellFromGitExe(gitExe string) string {
	dir := filepath.Dir(gitExe)
	parent := filepath.Dir(dir)
	switch strings.ToLower(filepath.Base(dir)) {
	case "cmd":
		return filepath.Join(parent, "bin", "sh.exe")
	case "bin":
		if isToolchainDir(filepath.Base(parent)) {
			return filepath.Join(filepath.Dir(parent), "bin", "sh.exe")
		}
		return filepath.Join(parent, "bin", "sh.exe")
	}
	return ""
}

// isToolchainDir 报告目录名是否是 Git for Windows 的工具链目录（mingw64、mingw32、clangarm64 等）。
func isToolchainDir(name string) bool {
	name = strings.ToLower(name)
	return strings.HasPrefix(name, "mingw") || strings.HasPrefix(name, "clang") || strings.HasPrefix(name, "ucrt")
}
