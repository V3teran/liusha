package logx

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	t.Setenv("LIUSHA_LOG_PROCESS", "proc-A")
	t.Setenv("LIUSHA_LOG_TO_STDOUT", "false")
	t.Setenv("LIUSHA_LOG_TO_FILE", "true")
	t.Setenv("LIUSHA_LOG_FORMAT", "json")
	t.Setenv("LIUSHA_INSTANCE", "pid-123@host-x")

	l := New("svcA")
	l.Info().Str("k", "v").Msg("first")

	// 文件名取自 LIUSHA_LOG_PROCESS（不再是 service 名）。
	path := filepath.Join(dir, "proc-A.log")
	raw, err := os.ReadFile(path)
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
	t.Setenv("LIUSHA_LOG_PROCESS", "proc-err")
	t.Setenv("LIUSHA_LOG_TO_STDOUT", "false")
	t.Setenv("LIUSHA_LOG_TO_FILE", "true")
	t.Setenv("LIUSHA_LOG_FORMAT", "json")

	l := New("svcErr")
	l.Error().Err(errors.New("boom")).Msg("oops")

	raw, err := os.ReadFile(filepath.Join(dir, "proc-err.log"))
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

// TestE2E_ProcessAggregation 模拟一个 cmd 进程内多个 logger（cmd 入口 + 多个包级 logger）
// 各自调用 logx.New("name") 后，日志聚合到 logs/{processName}.log 单文件，
// 通过 service JSON 字段区分上下文。这是对"包级 logger 不再碎片化"诉求的端到端校验。
func TestE2E_ProcessAggregation(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LIUSHA_LOG_DIR", dir)
	t.Setenv("LIUSHA_LOG_PROCESS", "runner-proc")
	t.Setenv("LIUSHA_LOG_TO_STDOUT", "false")
	t.Setenv("LIUSHA_LOG_TO_FILE", "true")
	t.Setenv("LIUSHA_LOG_FORMAT", "json")
	t.Setenv("LIUSHA_INSTANCE", "pid-x@host-y")

	// 模拟 runner 进程：cmd 入口 logger + 7 个包级 logger
	services := []string{
		"runner",
		"traffic", "vulnfinding", "reactrun",
		"llm.instrument", "llmcall.store", "tools.run_command",
	}
	for _, s := range services {
		l := New(s)
		l.Info().Str("k", s+"-marker").Msg("启动")
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
	raw, err := os.ReadFile(filepath.Join(dir, "runner-proc.log"))
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
