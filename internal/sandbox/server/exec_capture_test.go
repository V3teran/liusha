package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/V3teran/liusha/internal/sandbox"
)

// 两个流合计没有超过阈值：全部留在内存、不落盘（连 console/ 目录都不建）。
func TestOutputCapture_UnderThresholdKeepsEverythingInMemory(t *testing.T) {
	console := filepath.Join(t.TempDir(), consoleDirName)
	c := newOutputCapture(30, console)
	_, _ = c.stdout.Write([]byte(strings.Repeat("a", 20)))
	_, _ = c.stderr.Write([]byte(strings.Repeat("b", 10)))

	got := c.finish()
	if got.stdout != strings.Repeat("a", 20) || got.stderr != strings.Repeat("b", 10) ||
		got.stdoutBytes != 20 || got.stderrBytes != 10 || got.stdoutFile != "" || got.stderrFile != "" {
		t.Fatalf("合计没超过阈值应原样返回、不落盘: %+v", got)
	}
	if _, err := os.Stat(console); !os.IsNotExist(err) {
		t.Fatal("没有落盘就不该有 console/ 目录")
	}
}

// 两个流合计超过阈值：两个流都完整落盘——哪怕单独看哪一个都不算长。之后进模型视野的窗口是对
// 整份结果开的，只看单个流定不了「有没有内容被省掉」，所以阈值按合计算；落盘之后内存里只留
// 有界的尾部，响应照常带尾部。
func TestOutputCapture_CombinedThresholdSpillsBothStreams(t *testing.T) {
	keep := maxCaptureBytes
	maxCaptureBytes = 48
	t.Cleanup(func() { maxCaptureBytes = keep })

	c := newOutputCapture(30, filepath.Join(t.TempDir(), consoleDirName))
	chunk := bytes.Repeat([]byte("x"), 4096)
	_, _ = c.stdout.Write(bytes.Repeat([]byte("a"), 20))
	for range 64 { // stdout 256 KiB：远超阈值，也超过响应保留的上限
		_, _ = c.stdout.Write(chunk)
	}
	_, _ = c.stderr.Write([]byte(strings.Repeat("b", 20))) // 单独看只有 20 字节，不到阈值

	got := c.finish()
	want := strings.Repeat("a", 20) + strings.Repeat("x", 4096*64)
	if got.stdoutBytes != int64(len(want)) || got.stdoutFile == "" {
		t.Fatalf("stdout 应记下完整字节数并落盘: %d %q", got.stdoutBytes, got.stdoutFile)
	}
	if len(got.stdout) != 48 || !strings.HasSuffix(got.stdout, "xxxx") {
		t.Fatalf("超过响应上限的流只带末尾 48 字节: %d 字节", len(got.stdout))
	}
	assertFileContent(t, got.stdoutFile, want, "落盘文件里是完整的 stdout")
	if got.stderr != strings.Repeat("b", 20) || got.stderrFile == "" {
		t.Fatalf("合计超过阈值时 stderr 也落盘（哪怕它自己很短）: %q", got.stderrFile)
	}
	assertFileContent(t, got.stderrFile, strings.Repeat("b", 20), "落盘文件里是完整的 stderr")
	if got.stdoutErr != nil || got.stderrErr != nil || got.stdoutUnsaved != 0 || got.stderrUnsaved != 0 {
		t.Fatalf("落盘成功不该有失败或未留档: %+v", got)
	}
	if cap(c.stdout.keep.buf) != 48 {
		t.Fatalf("超过响应上限之后内存里应只有一个定长缓冲: cap=%d", cap(c.stdout.keep.buf))
	}
}

// 不设阈值（browser_drive 这类调用方）：全部返回、不落盘，与过去一致。
func TestOutputCapture_NoThresholdNeverSpills(t *testing.T) {
	console := filepath.Join(t.TempDir(), consoleDirName)
	c := newOutputCapture(0, console)
	all := strings.Repeat("a", 1000)
	_, _ = c.stdout.Write([]byte(all))

	got := c.finish()
	if got.stdout != all || got.stdoutBytes != 1000 || got.stdoutFile != "" {
		t.Fatalf("不设阈值应全部返回、不落盘: len=%d size=%d file=%q", len(got.stdout), got.stdoutBytes, got.stdoutFile)
	}
	if _, err := os.Stat(console); !os.IsNotExist(err) {
		t.Fatal("不设阈值不该有 console/ 目录")
	}
}

// 两个流各由一个协程送达，先后与命令写它们的顺序无关：先送到的小流在合计越过阈值时已经写完，
// 收集结束时要给它补落盘——否则它的文件时有时无，取决于调度。
func TestOutputCapture_StreamFinishedBeforeThresholdIsStillSaved(t *testing.T) {
	console := filepath.Join(t.TempDir(), consoleDirName)
	c := newOutputCapture(100, console)
	_, _ = c.stderr.Write([]byte("err\n"))                // stderr 先到，合计 4，没越过阈值
	_, _ = c.stdout.Write(bytes.Repeat([]byte("x"), 240)) // stdout 后到，越过阈值

	got := c.finish()
	if got.stderrFile == "" || got.stderr != "err\n" {
		t.Fatalf("先写完的流也应落盘: %q %q", got.stderr, got.stderrFile)
	}
	assertFileContent(t, got.stderrFile, "err\n", "补落盘的文件里是这个流的完整内容")
	if got.stderrUnsaved != 0 || got.stderrErr != nil {
		t.Fatalf("补落盘不该有未留档或失败: %+v", got)
	}
}

// 响应上限为 0（只有测试会这样注入）：什么都不保留，也不崩。
func TestOutputCapture_ZeroCaptureLimit(t *testing.T) {
	keep := maxCaptureBytes
	maxCaptureBytes = 0
	t.Cleanup(func() { maxCaptureBytes = keep })

	c := newOutputCapture(4, filepath.Join(t.TempDir(), consoleDirName))
	_, _ = c.stdout.Write([]byte("0123456789"))

	got := c.finish()
	if got.stdout != "" || got.stdoutBytes != 10 || got.stdoutFile == "" {
		t.Fatalf("响应不保留内容、完整字节数照记、照常落盘: %+v", got)
	}
}

// 响应里的尾部从字符边界起：只留末尾时窗口切在多字节字符中间，开头的残字节丢掉。
func TestOutputCapture_TailStartsOnRuneBoundary(t *testing.T) {
	keep := maxCaptureBytes
	maxCaptureBytes = 10
	t.Cleanup(func() { maxCaptureBytes = keep })

	c := newOutputCapture(5, filepath.Join(t.TempDir(), consoleDirName))
	_, _ = c.stdout.Write([]byte(strings.Repeat("中", 10))) // 30 字节，每个字 3 字节

	got := c.finish()
	if !utf8.ValidString(got.stdout) || got.stdout != strings.Repeat("中", 3) {
		t.Fatalf("尾部应从字符边界起（3 个字共 9 字节），实际 %q", got.stdout)
	}
}

// 落盘文件有上限：超过之后不再写，没写进去的字节数报给调用方（由它写进提示）。
func TestOutputCapture_SpillFileIsCapped(t *testing.T) {
	keep := maxSaveBytes
	maxSaveBytes = 100
	t.Cleanup(func() { maxSaveBytes = keep })

	c := newOutputCapture(10, filepath.Join(t.TempDir(), consoleDirName))
	_, _ = c.stdout.Write([]byte(strings.Repeat("a", 150)))

	got := c.finish()
	if got.stdoutBytes != 150 || got.stdoutUnsaved != 50 || got.stdoutErr != nil {
		t.Fatalf("文件应只留前 100 字节、其余 50 字节报为未留档: %+v", got)
	}
	assertFileContent(t, got.stdoutFile, strings.Repeat("a", 100), "落盘文件里是上限之内的部分")
}

// 落盘失败不丢输出：响应照常带尾部与完整字节数，只是没有文件，原因一并报出——完整输出少一份
// 留档，不该让这次命令的结果也跟着没了。
func TestOutputCapture_SpillFailureKeepsOutput(t *testing.T) {
	blocked := filepath.Join(t.TempDir(), consoleDirName)
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	c := newOutputCapture(4, blocked)
	_, _ = c.stdout.Write([]byte("0123456789"))

	got := c.finish()
	if got.stdout != "0123456789" || got.stdoutBytes != 10 || got.stdoutFile != "" || got.stdoutErr == nil {
		t.Fatalf("落盘失败应仍给出输出与完整字节数: %q %d %q %v", got.stdout, got.stdoutBytes, got.stdoutFile, got.stdoutErr)
	}
	if entries, _ := os.ReadDir(filepath.Dir(blocked)); len(entries) != 1 {
		t.Fatalf("落盘失败不该留下文件: %d 项", len(entries))
	}
}

// /exec 带落盘阈值：输出合计超过它时两个流完整落盘到任务目录的 console/ 下——操作员在文件面
// 看得到，命令自己也读得到；响应带完整字节数与落盘路径，输出本身原样返回。
func TestHandleExec_SavesFullOutputOverThreshold(t *testing.T) {
	h := newHarness(t)
	body, err := json.Marshal(sandbox.ExecRequest{
		AssignmentID: "asg", TaskID: "task", AgentRunID: "run",
		Command:             `printf 'out-%03d\n' $(seq 1 300); printf 'err\n' >&2`,
		TimeoutSeconds:      30,
		SaveOutputOverBytes: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	var res sandbox.ExecResult
	h.decode(t, http.MethodPost, "/exec", bytes.NewReader(body), &res)

	var full strings.Builder
	for i := 1; i <= 300; i++ {
		fmt.Fprintf(&full, "out-%03d\n", i)
	}
	if res.Stdout != full.String() || res.Stderr != "err\n" {
		t.Fatalf("输出应原样返回，不被沙盒截断: stdout %d 字节 stderr %q", len(res.Stdout), res.Stderr)
	}
	if res.StdoutBytes != int64(full.Len()) || res.StderrBytes != 4 {
		t.Fatalf("应给出两个流的完整字节数: stdout=%d（期望 %d） stderr=%d", res.StdoutBytes, full.Len(), res.StderrBytes)
	}
	if res.StdoutFile == "" || res.StderrFile == "" {
		t.Fatalf("合计超过阈值时两个流都应落盘: %q %q", res.StdoutFile, res.StderrFile)
	}
	assertFileContent(t, res.StdoutFile, full.String(), "落盘文件里是完整的 stdout")
	assertFileContent(t, res.StderrFile, "err\n", "落盘文件里是完整的 stderr")
	for _, f := range []string{res.StdoutFile, res.StderrFile} {
		rel, err := filepath.Rel(h.root, f)
		if err != nil || filepath.ToSlash(filepath.Dir(rel)) != "asg/task/console" {
			t.Fatalf("落盘位置应是任务目录下的 console/，实际 %q", rel)
		}
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("落盘成功不该有提示: %v", res.Warnings)
	}
}

// 合计没超过阈值：原样返回，不落盘。
func TestHandleExec_UnderThresholdDoesNotSave(t *testing.T) {
	h := newHarness(t)
	body, _ := json.Marshal(sandbox.ExecRequest{AssignmentID: "asg", TaskID: "task", AgentRunID: "run",
		Command: `printf 'out-%03d\n' $(seq 1 10)`, TimeoutSeconds: 30, SaveOutputOverBytes: 1000})
	var res sandbox.ExecResult
	h.decode(t, http.MethodPost, "/exec", bytes.NewReader(body), &res)

	if n := strings.Count(res.Stdout, "out-"); n != 10 || res.StdoutBytes != int64(len(res.Stdout)) || res.StdoutFile != "" {
		t.Fatalf("没超过阈值应原样返回、不落盘: %d 行 size=%d file=%q", n, res.StdoutBytes, res.StdoutFile)
	}
	if _, err := os.Stat(filepath.Join(h.root, "asg", "task", consoleDirName)); !os.IsNotExist(err) {
		t.Fatal("没超过阈值不该有 console/ 目录")
	}
}

// 不带落盘阈值（browser_drive 这类调用方）：与过去一致——输出原样返回、不落盘。
func TestHandleExec_NoThresholdKeepsLegacyBehaviour(t *testing.T) {
	h := newHarness(t)
	res := h.execTask(t, "legacy", `printf 'out-%03d\n' $(seq 1 300)`)
	if n := strings.Count(res.Stdout, "out-"); n != 300 || !strings.HasPrefix(res.Stdout, "out-001\n") {
		t.Fatalf("不带阈值应返回完整输出，实际 %d 行", n)
	}
	if res.StdoutBytes != int64(len(res.Stdout)) || res.StdoutFile != "" {
		t.Fatalf("不带阈值: 完整字节数等于返回长度、不落盘: %d %q", res.StdoutBytes, res.StdoutFile)
	}
	if _, err := os.Stat(filepath.Join(h.root, "asg", "legacy", consoleDirName)); !os.IsNotExist(err) {
		t.Fatal("不带阈值不该有 console/ 目录")
	}
}

// 落盘阈值为负是请求错误。
func TestHandleExec_NegativeThresholdIsRejected(t *testing.T) {
	h := newHarness(t)
	body, _ := json.Marshal(sandbox.ExecRequest{AssignmentID: "asg", TaskID: "task", AgentRunID: "run",
		Command: "echo hi", TimeoutSeconds: 5, SaveOutputOverBytes: -1})
	resp, err := h.do(http.MethodPost, "/exec", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("负的落盘阈值应 400，实际 %d", resp.StatusCode)
	}
}

// 落盘失败时响应仍带输出与完整字节数，没有文件路径，提示里说明原因。
func TestHandleExec_SpillFailureIsReported(t *testing.T) {
	h := newHarness(t)
	blocked := filepath.Join(h.root, "asg", "task", consoleDirName)
	if err := os.MkdirAll(filepath.Dir(blocked), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(blocked, []byte("not a dir"), 0o600); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(sandbox.ExecRequest{AssignmentID: "asg", TaskID: "task", AgentRunID: "run",
		Command: `printf 'out-%03d\n' $(seq 1 300)`, TimeoutSeconds: 30, SaveOutputOverBytes: 100})
	var res sandbox.ExecResult
	h.decode(t, http.MethodPost, "/exec", bytes.NewReader(body), &res)

	if res.StdoutBytes != 2400 || len(res.Stdout) != 2400 || res.StdoutFile != "" {
		t.Fatalf("落盘失败应仍给出完整输出与字节数、没有文件: size=%d len=%d file=%q", res.StdoutBytes, len(res.Stdout), res.StdoutFile)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "stdout") || !strings.Contains(res.Warnings[0], consoleDirName) {
		t.Fatalf("落盘失败应在提示里说明: %v", res.Warnings)
	}
}

func assertFileContent(t *testing.T, path, want, why string) {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 // 测试临时目录里的落盘文件
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != want {
		t.Fatalf("%s: 文件 %d 字节，期望 %d 字节", why, len(data), len(want))
	}
}
