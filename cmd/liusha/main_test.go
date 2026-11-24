package main

import (
	"bytes"
	"os"
	"path/filepath"
	"slices"
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

// 子命令都接受 -config：指定的配置文件不存在是部署错误，必须在启动任何组件之前报出。
func TestRun_ConfigFlagReachesLoader(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for _, c := range commands {
		code, _, stderr := runCLI(t, c.name, "-config", missing)
		if code != 1 || !strings.Contains(stderr, "加载配置") {
			t.Fatalf("%s: code=%d stderr=%q", c.name, code, stderr)
		}
	}
}

// 不给 -config 也没有 LIUSHA_CONFIG 时读程序所在目录的 config.yaml（二进制部署的摆放）；
// 那里没有就报错，不用任何内置配置顶替。
func TestRun_ConfigBesideExecutable(t *testing.T) {
	t.Setenv("LIUSHA_CONFIG", "")
	want, err := besideExecutable("config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, "migrate")
	if code != 1 || !strings.Contains(stderr, want) {
		t.Fatalf("应读取程序所在目录的 %s: code=%d stderr=%q", want, code, stderr)
	}
}

// 位置参数：命令不接受，在连接任何外部依赖之前报错。
func TestRun_PositionalArgs(t *testing.T) {
	t.Setenv("LIUSHA_CONFIG", filepath.Join("..", "..", "internal", "config", "config.yaml"))
	if code, _, stderr := runCLI(t, "migrate", "extra"); code != 1 || !strings.Contains(stderr, "不接受参数") {
		t.Fatalf("migrate extra: code=%d stderr=%q", code, stderr)
	}
}

// editedTemplate 把仓库里的配置模板做过修改后写到临时文件；每处 old 必须恰好出现一次，防止模板
// 改动后测试悄悄失效。
func editedTemplate(t *testing.T, edits ...[2]string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("..", "..", "internal", "config", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(b)
	for _, e := range edits {
		if n := strings.Count(body, e[0]); n != 1 {
			t.Fatalf("模板中 %q 出现 %d 次，期望 1 次", e[0], n)
		}
		body = strings.Replace(body, e[0], e[1], 1)
	}
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// 沙盒容器里的程序可能与主程序不同版本（多镜像部署：沙盒镜像由部署方本地构建，升级主程序后
// 常常还是旧的），而配置文件是随主程序更新的。沙盒只要求自己读的键齐全：别的段加了键、删了键、
// 取值变得对它无意义，它照常启动；自己读的键缺失或取值无效，仍然拒绝启动。
// 探针是多余的位置参数：配置加载通过后，子命令在连接任何外部依赖之前报「不接受参数」。
func TestRun_SandboxLoadsOnlyTheKeysItReads(t *testing.T) {
	started := func(t *testing.T, edits ...[2]string) string {
		t.Helper()
		t.Setenv("LIUSHA_CONFIG", editedTemplate(t, edits...))
		code, _, stderr := runCLI(t, "sandbox", "extra")
		if code != 1 || !strings.Contains(stderr, "不接受参数") {
			t.Fatalf("沙盒应已通过配置加载并报「不接受参数」，实际 code=%d stderr=%q", code, stderr)
		}
		return stderr
	}
	// 缺失必须报「缺少键」（指向同版本的模板），不能只是碰巧被取值校验报出同一个键。
	refused := func(t *testing.T, edits [][2]string, wants ...string) {
		t.Helper()
		t.Setenv("LIUSHA_CONFIG", editedTemplate(t, edits...))
		code, _, stderr := runCLI(t, "sandbox", "extra")
		for _, want := range append([]string{"加载配置"}, wants...) {
			if code != 1 || !strings.Contains(stderr, want) {
				t.Fatalf("沙盒应拒绝启动，报错里应有 %q，实际 code=%d stderr=%q", want, code, stderr)
			}
		}
	}

	t.Run("别的段怎么变都不管", func(t *testing.T) {
		started(t, [2]string{"  concurrency: 3", "  step_llm_timeout_seconds: 300\n  concurrency: 3"}) // 加键：改回改名前的旧键
		started(t, [2]string{"  concurrency: 3                     # 同时执行的任务数\n", ""})                 // 删键
		started(t, [2]string{"  concurrency: 3", "  concurrency: 0"})                                  // 取值无效
		started(t, [2]string{"  read_timeout_seconds: 15", "  read_timeout_seconds: 十五"})              // 类型不对
	})
	t.Run("自己读的键缺失仍然拒绝", func(t *testing.T) {
		refused(t, [][2]string{{"  viewport_width: 1280", "  viewport_width:"}}, "缺少键", "sandbox.viewport_width") // 值为空等同于没写
		refused(t, [][2]string{{"  url: http://127.0.0.1:8590", "  url:"}}, "缺少键", "runner.url")
		refused(t, [][2]string{{"  callback_host: \"\"\n", ""}}, "缺少键", "sandbox.callback_host") // 允许为空的键
		refused(t, [][2]string{{"data_dir: ~/data/liusha\n", ""}}, "缺少键", "data_dir")
	})
	t.Run("自己读的键取值无效仍然拒绝", func(t *testing.T) {
		refused(t, [][2]string{{"  viewport_width: 1280", "  viewport_width: 0"}}, "sandbox.viewport_width")
		refused(t, [][2]string{{"data_dir: ~/data/liusha", "data_dir: data/liusha"}}, "data_dir")
	})
}

// 整份加载的子命令不变：配置文件与程序版本不一致（别的段多了不认识的键）仍然拒绝启动。
func TestRun_OtherCommandsStillLoadTheWholeFile(t *testing.T) {
	t.Setenv("LIUSHA_CONFIG", editedTemplate(t, [2]string{"  concurrency: 3", "  step_llm_timeout_seconds: 300\n  concurrency: 3"}))
	code, _, stderr := runCLI(t, "migrate", "extra")
	if code != 1 || !strings.Contains(stderr, "加载配置") || !strings.Contains(stderr, "step_llm_timeout_seconds") {
		t.Fatalf("migrate 应因未知键拒绝启动，实际 code=%d stderr=%q", code, stderr)
	}
}

// 启动流程在子命令运行之前读数据目录与日志配置：声明了视角的子命令，视角必须覆盖这几处
// （经 config.ProcessView 构造即满足）。
func TestCommandViewsCoverWhatStartupReads(t *testing.T) {
	views := 0
	for _, c := range commands {
		if c.view == nil {
			continue
		}
		views++
		for _, key := range []string{"data_dir", "log.level", "log.max_size_bytes"} {
			covers := slices.ContainsFunc(c.view, func(p string) bool { return key == p || strings.HasPrefix(key, p+".") })
			if !covers {
				t.Errorf("%s 的视角没有覆盖启动流程读的键 %s：视角经 config.ProcessView 构造", c.name, key)
			}
		}
	}
	if views == 0 {
		t.Fatal("没有子命令声明视角：这个守护无事可守，连同 command.view 一起删掉")
	}
}

// -env-file 先于配置加载生效：文件里指定的配置文件路径被采用。
func TestRun_EnvFileAppliedBeforeConfig(t *testing.T) {
	t.Setenv("LIUSHA_CONFIG", "")
	_ = os.Unsetenv("LIUSHA_CONFIG")
	dir := t.TempDir()
	missing := filepath.Join(dir, "from-env-file.yaml")
	envPath := filepath.Join(dir, ".env")
	if err := os.WriteFile(envPath, []byte("LIUSHA_CONFIG="+missing+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := runCLI(t, "migrate", "-env-file", envPath)
	if code != 1 || !strings.Contains(stderr, "from-env-file.yaml") {
		t.Fatalf("应按环境变量文件里的 LIUSHA_CONFIG 加载配置: code=%d stderr=%q", code, stderr)
	}
	if code, _, stderr := runCLI(t, "migrate", "-env-file", filepath.Join(dir, "nope.env")); code != 1 || !strings.Contains(stderr, "环境变量文件") {
		t.Fatalf("环境变量文件不存在应报错: code=%d stderr=%q", code, stderr)
	}
}
