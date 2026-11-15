package sandbox

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

// fakeFS 模拟一组存在的可执行文件。
type fakeFS map[string]bool

func (f fakeFS) lookPath(name string) (string, error) {
	for path := range f {
		if filepath.Base(path) == name {
			return path, nil
		}
	}
	return "", errors.New("not found")
}

func (f fakeFS) exists(path string) bool { return f[path] }

// 显式配置只按配置查找：找到即用，找不到报错，不擅自换成别的 shell。
func TestResolveShell_Configured(t *testing.T) {
	fs := fakeFS{"/usr/bin/dash": true, "/bin/sh": true}
	got, err := resolveShell("dash", shellLookup{lookPath: fs.lookPath, exists: fs.exists})
	if err != nil || got != "/usr/bin/dash" {
		t.Fatalf("got %q %v", got, err)
	}
	if _, err := resolveShell("zsh-missing", shellLookup{lookPath: fs.lookPath, exists: fs.exists}); err == nil {
		t.Fatal("显式配置的 shell 找不到时应报错")
	}
}

// 未配置：先用 PATH 里的 sh，没有再依次试平台候选（Windows 上是 Git for Windows 的位置）。
func TestResolveShell_Automatic(t *testing.T) {
	candidates := func() []string { return []string{"/opt/git-a/bin/sh.exe", "/opt/git-b/bin/sh.exe"} }

	fs := fakeFS{"/bin/sh": true, "/opt/git-b/bin/sh.exe": true}
	if got, _ := resolveShell("", shellLookup{lookPath: fs.lookPath, exists: fs.exists, candidates: candidates}); got != "/bin/sh" {
		t.Fatalf("PATH 里有 sh 时应优先用它, got %q", got)
	}

	fs = fakeFS{"/opt/git-b/bin/sh.exe": true}
	if got, _ := resolveShell("", shellLookup{lookPath: fs.lookPath, exists: fs.exists, candidates: candidates}); got != "/opt/git-b/bin/sh.exe" {
		t.Fatalf("应找到第一个存在的候选, got %q", got)
	}

	_, err := resolveShell("", shellLookup{lookPath: fakeFS{}.lookPath, exists: fakeFS{}.exists, candidates: candidates})
	if err == nil || !strings.Contains(err.Error(), "sandbox.shell") {
		t.Fatalf("都找不到时应报错并说明如何配置, got %v", err)
	}
}

// 由 git 可执行文件推出安装根：Git for Windows 把 git 放在 <根>\cmd、<根>\bin 或
// <根>\mingw64\bin 下，sh 在 <根>\bin\sh.exe。
func TestGitShellFromGitExe(t *testing.T) {
	for git, want := range map[string]string{
		filepath.Join("C:", "Git", "cmd", "git.exe"):               filepath.Join("C:", "Git", "bin", "sh.exe"),
		filepath.Join("C:", "Git", "bin", "git.exe"):               filepath.Join("C:", "Git", "bin", "sh.exe"),
		filepath.Join("C:", "Git", "mingw64", "bin", "git.exe"):    filepath.Join("C:", "Git", "bin", "sh.exe"),
		filepath.Join("C:", "Git", "clangarm64", "bin", "git.exe"): filepath.Join("C:", "Git", "bin", "sh.exe"),
		filepath.Join("C:", "tools", "git.exe"):                    "",
	} {
		if got := gitShellFromGitExe(git); got != want {
			t.Errorf("gitShellFromGitExe(%q) = %q，期望 %q", git, got, want)
		}
	}
}
