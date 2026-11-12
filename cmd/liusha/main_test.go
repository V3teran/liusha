package main

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"
)

func runCLI(t *testing.T, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code = run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

// 无命令 / 未知命令是用法错误：退出码 2，并列出全部命令。
func TestRun_UsageErrors(t *testing.T) {
	for _, args := range [][]string{nil, {"nope"}} {
		code, _, stderr := runCLI(t, args...)
		if code != 2 {
			t.Fatalf("%v: 退出码 = %d，期望 2", args, code)
		}
		for _, c := range commands {
			if !strings.Contains(stderr, c.name) {
				t.Fatalf("%v: 用法应列出命令 %s:\n%s", args, c.name, stderr)
			}
		}
	}
}

func TestRun_HelpAndVersion(t *testing.T) {
	if code, stdout, _ := runCLI(t, "help"); code != 0 || !strings.Contains(stdout, "serve") {
		t.Fatalf("help: code=%d out=%q", code, stdout)
	}
	if code, stdout, _ := runCLI(t, "version"); code != 0 || strings.TrimSpace(stdout) != version {
		t.Fatalf("version: code=%d out=%q", code, stdout)
	}
}

// 子命令都接受 -config：指定的覆盖文件不存在是部署错误，必须在启动任何组件之前报出。
func TestRun_ConfigFlagReachesLoader(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for _, c := range commands {
		code, _, stderr := runCLI(t, c.name, "-config", missing)
		if code != 1 || !strings.Contains(stderr, "加载配置") {
			t.Fatalf("%s: code=%d stderr=%q", c.name, code, stderr)
		}
	}
}

// 位置参数：组件命令不接受，corpus-import 要求至少一个文件。均在连接任何外部依赖之前报错。
func TestRun_PositionalArgs(t *testing.T) {
	t.Setenv("LIUSHA_CONFIG", "")
	if code, _, stderr := runCLI(t, "migrate", "extra"); code != 1 || !strings.Contains(stderr, "不接受参数") {
		t.Fatalf("migrate extra: code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runCLI(t, "corpus-import"); code != 1 || !strings.Contains(stderr, "缺少要导入的文件") {
		t.Fatalf("corpus-import: code=%d stderr=%q", code, stderr)
	}
}
