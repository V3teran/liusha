package logx

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
)

func TestNew_JSONIncludesService(t *testing.T) {
	var buf bytes.Buffer
	l := newWith(&buf, "json").With().Str("service", "test_pkg").Logger()
	l.Info().Str("k", "v").Msg("hello")

	var got map[string]any
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("expect JSON, got %q: %v", buf.String(), err)
	}
	if got["service"] != "test_pkg" || got["message"] != "hello" || got["k"] != "v" {
		t.Fatalf("missing fields: %v", got)
	}
}

func TestNew_DevelopmentIsConsole(t *testing.T) {
	var buf bytes.Buffer
	l := newWith(&buf, "development")
	l.Info().Msg("plain")
	if !strings.Contains(buf.String(), "plain") {
		t.Fatalf("console writer should contain message verbatim, got %q", buf.String())
	}
	if json.Valid(buf.Bytes()) {
		t.Fatalf("development writer should not be JSON, got %q", buf.String())
	}
}

func TestNew_FileWriteAndFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LIUSHA_LOG_DIR", dir)
	SetProcess("proc-A")
	t.Cleanup(func() { SetProcess("") })
	t.Setenv("LIUSHA_LOG_TO_STDOUT", "false")
	t.Setenv("LIUSHA_LOG_TO_FILE", "true")
	t.Setenv("LIUSHA_LOG_FORMAT", "json")
	t.Setenv("LIUSHA_INSTANCE", "pid-123@host-x")

	l := New("svcA")
	l.Info().Str("k", "v").Msg("first")

	// 文件名取自 SetProcess 设定的进程名（不是 service 名）。
	path := filepath.Join(dir, "proc-A.log")
	raw, err := os.ReadFile(path) // #nosec G304 // 路径来自进程配置/种子目录，非用户输入
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	line := strings.TrimSpace(string(raw))
	if line == "" {
		t.Fatalf("log file empty")
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("expect JSON, got %q: %v", line, err)
	}
	if got["service"] != "svcA" {
		t.Fatalf("service = %v, want svcA", got["service"])
	}
	if got["instance"] != "pid-123@host-x" {
		t.Fatalf("instance = %v", got["instance"])
	}
	if got["message"] != "first" {
		t.Fatalf("message = %v", got["message"])
	}
	if got["k"] != "v" {
		t.Fatalf("custom field missing: %v", got)
	}
	caller, _ := got["caller"].(string)
	if !strings.Contains(caller, "logger_test.go:") {
		t.Fatalf("caller should point to test file, got %q", caller)
	}
}

func TestNew_ErrorFieldAttached(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LIUSHA_LOG_DIR", dir)
	SetProcess("proc-err")
	t.Cleanup(func() { SetProcess("") })
	t.Setenv("LIUSHA_LOG_TO_STDOUT", "false")
	t.Setenv("LIUSHA_LOG_TO_FILE", "true")
	t.Setenv("LIUSHA_LOG_FORMAT", "json")

	l := New("svcErr")
	l.Error().Err(errors.New("boom")).Msg("oops")

	raw, err := os.ReadFile(filepath.Join(dir, "proc-err.log")) // #nosec G304 // 路径来自进程配置/种子目录，非用户输入
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(raw), &got); err != nil {
		t.Fatalf("expect JSON, got %q: %v", raw, err)
	}
	if got["error"] != "boom" {
		t.Fatalf("error field = %v", got["error"])
	}
	if got["level"] != "error" {
		t.Fatalf("level = %v", got["level"])
	}
}

func TestShortCaller(t *testing.T) {
	cases := []struct {
		in   string
		line int
		want string
	}{
		{"/a/b/c/pkg/file.go", 42, "pkg/file.go:42"},
		{"pkg/file.go", 1, "pkg/file.go:1"},
		{"file.go", 7, "file.go:7"},
		{"/file.go", 9, "file.go:9"},
	}
	for _, c := range cases {
		if got := shortCaller(0, c.in, c.line); got != c.want {
			t.Errorf("shortCaller(%q, %d) = %q, want %q", c.in, c.line, got, c.want)
		}
	}
}

func TestParseLevelDefaults(t *testing.T) {
	if parseLevel("").String() != "info" {
		t.Fatalf("default level should be info")
	}
	if parseLevel("WARN").String() != "warn" {
		t.Fatalf("warn parse failed")
	}
}

// TestE2E_ProcessAggregation 模拟一个进程内多个 logger（入口 + 多个包级 logger）
// 各自调用 logx.New("name") 后，日志聚合到 logs/{进程名}.log 单文件，
// 通过 service JSON 字段区分上下文。包级 logger 在入口 SetProcess 之前就已建好
// （包初始化期），仍须落到入口设定的文件——文件在首次写入时才确定。
func TestE2E_ProcessAggregation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LIUSHA_LOG_DIR", dir)
	t.Setenv("LIUSHA_LOG_TO_STDOUT", "false")
	t.Setenv("LIUSHA_LOG_TO_FILE", "true")
	t.Setenv("LIUSHA_LOG_FORMAT", "json")
	t.Setenv("LIUSHA_INSTANCE", "pid-x@host-y")
	t.Cleanup(func() { SetProcess("") })

	// 模拟 runner 进程：入口 logger + 6 个包级 logger，包级 logger 先于 SetProcess 构造
	services := []string{
		"runner",
		"traffic", "finding", "cognition",
		"llm.instrument", "llminvocation.store", "tools.run_command",
	}
	loggers := make([]zerolog.Logger, len(services))
	for i, s := range services {
		loggers[i] = New(s)
	}
	SetProcess("runner-proc")
	for i, s := range services {
		loggers[i].Info().Str("k", s+"-marker").Msg("启动")
	}

	// 应只产生 1 个文件
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != "runner-proc.log" {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		t.Fatalf("want exactly 1 file [runner-proc.log], got %v", names)
	}

	// 文件中应有 N 行，按 service 字段索引
	raw, err := os.ReadFile(filepath.Join(dir, "runner-proc.log")) // #nosec G304 // 路径来自进程配置/种子目录，非用户输入
	if err != nil {
		t.Fatalf("read aggregated log: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != len(services) {
		t.Fatalf("want %d lines, got %d:\n%s", len(services), len(lines), raw)
	}

	bySvc := map[string]map[string]any{}
	for _, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatalf("not JSON: %q: %v", line, err)
		}
		svc, _ := got["service"].(string)
		bySvc[svc] = got
	}
	for _, want := range services {
		got, ok := bySvc[want]
		if !ok {
			t.Errorf("missing entry for service=%s", want)
			continue
		}
		if got["instance"] != "pid-x@host-y" {
			t.Errorf("service=%s instance = %v", want, got["instance"])
		}
		if got["k"] != want+"-marker" {
			t.Errorf("service=%s marker leak: got %v", want, got["k"])
		}
		if got["message"] != "启动" {
			t.Errorf("service=%s message = %v", want, got["message"])
		}
	}
}

// newWith 仅供本包单测注入 writer 与显式形态使用，不读 LIUSHA_LOG_DIR/TO_FILE。
func newWith(w io.Writer, env string) zerolog.Logger {
	applyGlobals(loadConfig())
	if env == "development" {
		return zerolog.New(newConsoleWriter(w, true)).With().Timestamp().Logger()
	}
	return zerolog.New(w).With().Timestamp().Logger()
}

// 测试二进制里文件落盘缺省关闭：包级 logger 在测试启动即初始化，缺省相对目录 logs
// 会在每个被测包目录下生成 logs/*.test.log 产物。显式 LIUSHA_LOG_TO_FILE 仍然生效。
func TestLoadConfig_FileSinkOffByDefaultUnderTest(t *testing.T) {
	t.Setenv("LIUSHA_LOG_TO_FILE", "")
	if loadConfig().toFile {
		t.Fatal("go test 下文件落盘缺省应关闭")
	}
	t.Setenv("LIUSHA_LOG_TO_FILE", "true")
	if !loadConfig().toFile {
		t.Fatal("显式 LIUSHA_LOG_TO_FILE=true 应开启文件落盘")
	}
}

// 包级 logger 在入口补齐环境（liusha 的 -env-file）之前就已建好：输出端须按 SetProcess 时的
// 环境构建，而不是 logger 创建时的环境。
func TestNew_OutputFollowsEnvironmentAtSetProcess(t *testing.T) {
	early := New("pkg-level")

	dir := t.TempDir()
	t.Setenv("LIUSHA_LOG_DIR", dir)
	t.Setenv("LIUSHA_LOG_TO_STDOUT", "false")
	t.Setenv("LIUSHA_LOG_TO_FILE", "true")
	SetProcess("late-env")
	t.Cleanup(func() { SetProcess("") })

	early.Info().Msg("after env")
	raw, err := os.ReadFile(filepath.Join(dir, "late-env.log")) // #nosec G304 // 测试临时目录
	if err != nil || !strings.Contains(string(raw), "after env") {
		t.Fatalf("先建的 logger 应写入后设定的日志目录: %v %q", err, raw)
	}
}

// 输出端构建（首条日志、SetProcess 之后）与其它 goroutine 的写日志、建 logger 并发安全。
func TestOutput_ConcurrentBuildIsRaceFree(t *testing.T) {
	t.Setenv("LIUSHA_LOG_TO_STDOUT", "false")
	SetProcess("race")
	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l := New("race")
			l.Info().Int("i", i).Msg("x")
			l.Error().Err(errors.New("e")).Msg("y")
		}()
	}
	wg.Wait()
}
