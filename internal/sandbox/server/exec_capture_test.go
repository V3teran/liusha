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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/sandbox"
)

// 两个流合计没有超过阈值：全部留在内存、不落盘。
func TestOutputCapture_UnderThresholdKeepsEverythingInMemory(t *testing.T) {
	outDir := t.TempDir()
	c := newOutputCapture(30, outDir)
	_, _ = c.stdout.Write([]byte(strings.Repeat("a", 20)))
	_, _ = c.stderr.Write([]byte(strings.Repeat("b", 10)))

	got := c.finish()
	if got.stdout != strings.Repeat("a", 20) || got.stderr != strings.Repeat("b", 10) ||
		got.stdoutBytes != 20 || got.stderrBytes != 10 || got.stdoutFile != "" || got.stderrFile != "" {
		t.Fatalf("合计没超过阈值应原样返回、不落盘: %+v", got)
	}
	if got.stdoutUnsaved != 0 || got.stderrUnsaved != 0 {
		t.Fatalf("没落盘的流没有「没写进文件」可言，不该报未留档: %+v", got)
	}
	if captures := captureFilesIn(t, outDir); len(captures) != 0 {
		t.Fatal("没有落盘就不该写任何文件")
	}
}

// 两个流合计超过阈值：两个流都完整落盘——哪怕单独看哪一个都不算长。之后进模型视野的窗口是对
// 整份结果开的，只看单个流定不了「有没有内容被省掉」，所以阈值按合计算；落盘之后内存里只留
// 有界的尾部，响应照常带尾部。
func TestOutputCapture_CombinedThresholdSpillsBothStreams(t *testing.T) {
	keep := maxCaptureBytes
	maxCaptureBytes = 48
	t.Cleanup(func() { maxCaptureBytes = keep })

	c := newOutputCapture(30, t.TempDir())
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
	outDir := t.TempDir()
	c := newOutputCapture(0, outDir)
	all := strings.Repeat("a", 1000)
	_, _ = c.stdout.Write([]byte(all))

	got := c.finish()
	if got.stdout != all || got.stdoutBytes != 1000 || got.stdoutFile != "" {
		t.Fatalf("不设阈值应全部返回、不落盘: len=%d size=%d file=%q", len(got.stdout), got.stdoutBytes, got.stdoutFile)
	}
	if captures := captureFilesIn(t, outDir); len(captures) != 0 {
		t.Fatal("不设阈值就不该写任何文件")
	}
}

// 两个流各由一个协程送达，先后与命令写它们的顺序无关：先送到的小流在合计越过阈值时已经写完，
// 收集结束时要给它补落盘——否则它的文件时有时无，取决于调度。
func TestOutputCapture_StreamFinishedBeforeThresholdIsStillSaved(t *testing.T) {
	outDir := t.TempDir()
	c := newOutputCapture(100, outDir)
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

	c := newOutputCapture(4, t.TempDir())
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

	c := newOutputCapture(5, t.TempDir())
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

	c := newOutputCapture(10, t.TempDir())
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
	// out/ 本该是目录；把它造成一个文件，MkdirAll 必然失败——这是「落盘失败」的确定性注入。
	blocked := filepath.Join(t.TempDir(), "out")
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

// /exec 带落盘阈值：输出合计超过它时两个流完整落盘到任务产物目录 out/ 顶层（时间戳命名）——
// 操作员在工作空间页看得到，命令自己也读得到；响应带完整字节数与落盘路径，输出本身原样返回。
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
		if err != nil || filepath.ToSlash(filepath.Dir(rel)) != "asg/task/out" || !isCaptureFile(filepath.Base(rel)) {
			t.Fatalf("留档应直接落在任务产物目录 out/ 顶层（时间戳命名），实际 %q", rel)
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
	if captures := captureFilesIn(t, filepath.Join(h.root, "asg", "task", "out")); len(captures) != 0 {
		t.Fatal("没超过阈值不该有留档文件")
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("没超过阈值不该有任何提示: %v", res.Warnings)
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
	if captures := captureFilesIn(t, filepath.Join(h.root, "asg", "legacy", "out")); len(captures) != 0 {
		t.Fatal("不带阈值不该有留档文件")
	}
	if len(res.Warnings) != 0 {
		t.Fatalf("不带阈值不该有任何提示: %v", res.Warnings)
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

// 留档失败与超出留档上限，都写成结果里的提示：这次命令的输出照常返回，只是完整输出少了一份
// 留档——说明白，不悄悄少。
func TestStreamWarnings(t *testing.T) {
	got := streamWarnings(capturedStreams{stdoutErr: os.ErrPermission})
	if len(got) != 1 || !strings.Contains(got[0], "stdout") || !strings.Contains(got[0], "could not be saved") {
		t.Fatalf("留档失败应说明: %v", got)
	}
	got = streamWarnings(capturedStreams{stdoutUnsaved: 512})
	if len(got) != 1 || !strings.Contains(got[0], "512 bytes were not saved") {
		t.Fatalf("超出留档上限应说明没写进去多少: %v", got)
	}
	if got := streamWarnings(capturedStreams{}); len(got) != 0 {
		t.Fatalf("没有问题不该有提示: %v", got)
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

// 留档文件名有确切的形状，publish 据此跳过收编。
func TestIsCaptureFile(t *testing.T) {
	for name, want := range map[string]bool{
		"20261128-071052-3648473974.stdout": true,
		"20261128-071052-1.stderr":          true,
		"nmap_result.stdout":                false,
		"20261128-071052-1.log":             false,
		"stdout":                            false,
		"a.txt":                             false,
	} {
		assert.Equal(t, want, isCaptureFile(name), name)
	}
}

// captureFilesIn 列出目录下的留档文件名。
func captureFilesIn(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if isCaptureFile(e.Name()) {
			out = append(out, e.Name())
		}
	}
	return out
}

// 留档直接落在 out/ 顶层之后，后续执行的暂存目录会把它预置进来：publish 按名字跳过收编——
// 留档不是产物，不该被 base64 进后续执行的结果；留档本体也不该被动过。
func TestHandleExec_CaptureIsNotAProductOfLaterExecs(t *testing.T) {
	h := newHarness(t)
	body, _ := json.Marshal(sandbox.ExecRequest{AssignmentID: "asg", TaskID: "task", AgentRunID: "run",
		Command: `printf 'out-%03d
' $(seq 1 30)`, TimeoutSeconds: 30, SaveOutputOverBytes: 100})
	var res sandbox.ExecResult
	h.decode(t, http.MethodPost, "/exec", bytes.NewReader(body), &res)
	captures := captureFilesIn(t, filepath.Join(h.root, "asg", "task", "out"))
	require.Len(t, captures, 1, "输出超过阈值应有留档")

	res2 := h.execTask(t, "task", `ls`)
	if len(res2.Files) != 0 {
		t.Fatalf("留档不是产物，不得被后续执行收编: %+v", res2.Files)
	}
	assertFileContent(t, filepath.Join(h.root, "asg", "task", "out", captures[0]), res.Stdout, "留档本体不被动")
}
