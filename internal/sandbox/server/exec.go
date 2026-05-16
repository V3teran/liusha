package server

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// handleExec 处理 POST /exec：sh -c 命令 + OUTPUT_DIR 附件机制。
//
// 流程：
//  1. 解析 ExecRequest，校验必填
//  2. 创建独立目录 /tmp/exec-<id>/output（注入 $OUTPUT_DIR 环境变量）
//  3. exec.CommandContext 跑 sh -c（带 timeout）
//  4. 扫描 output 目录 b64 编码 + 应用上限（命令崩溃也扫，半成品有诊断价值）
//  5. defer 销毁临时目录
//
// 并发安全：每次 /exec 独立 uuid 目录，多请求互不干扰。
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	var req sandbox.ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode request: %v", err)
		return
	}
	if req.Command == "" {
		writeError(w, http.StatusBadRequest, "command required")
		return
	}
	if req.TimeoutSeconds <= 0 {
		writeError(w, http.StatusBadRequest, "timeout_seconds must be > 0")
		return
	}

	// 独立工作目录，每次 /exec 隔离避免并发污染
	execID := newExecID()
	workDir := filepath.Join(os.TempDir(), "exec-"+execID)
	outputDir := filepath.Join(workDir, "output")
	if err := os.MkdirAll(outputDir, 0o777); err != nil {
		writeError(w, http.StatusInternalServerError, "create workdir: %v", err)
		return
	}
	defer os.RemoveAll(workDir)

	// 命令超时控制——r.Context() 让客户端断开/取消能传到 sh 子进程
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	cmdCtx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", req.Command)
	cmd.Env = append(os.Environ(), "OUTPUT_DIR="+outputDir)

	// 让 sh 成为新进程组 leader；ctx 超时时 cmd.Cancel 杀整个进程组——
	// 防止 sh 被 SIGKILL 后子进程（sqlmap/tail/...）孤儿化继续持有 stdout pipe，
	// 导致 cmd.Wait() 阻塞至子进程自然结束（e2e 实测踩过 600s timeout 但 wall time 28min）。
	// 用负 PID 是 POSIX kill(2) 进程组语义。
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()

	res := sandbox.ExecResult{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			res.ExitCode = ee.ExitCode()
		} else {
			res.ExitCode = -1
		}
	}
	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		res.TimedOut = true
	}

	// 扫描产物——命令崩溃也扫，半成品有诊断价值
	files, warnings := collectAttachments(outputDir)
	res.Files = files
	res.Warnings = warnings

	writeJSON(w, http.StatusOK, res)
}

// collectAttachments 扫描 outputDir 顶层（不递归），返回 b64 编码的附件列表。
//
// 应用上限：
//   - 单文件 > maxFileSize：返回 warning，不入 files
//   - 总大小 > maxTotalSize：剩余文件全 warning
//   - 数量 > maxFileCount：剩余文件全 warning
//   - 子目录：返回 warning（不递归扫描，避免 chrome cache 等噪音）
//
// LLM 想保留产物的命令把文件写到 $OUTPUT_DIR 顶层即可。
func collectAttachments(outputDir string) ([]sandbox.Attachment, []string) {
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return nil, []string{fmt.Sprintf("scan output dir: %v", err)}
	}

	var (
		files    []sandbox.Attachment
		warnings []string
		total    int64
	)
	for _, e := range entries {
		if e.IsDir() {
			warnings = append(warnings, fmt.Sprintf("subdirectory '%s' skipped (not recursive)", e.Name()))
			continue
		}
		if len(files) >= maxFileCount {
			warnings = append(warnings, fmt.Sprintf("file '%s' skipped (count limit %d reached)", e.Name(), maxFileCount))
			continue
		}
		info, err := e.Info()
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("stat '%s': %v", e.Name(), err))
			continue
		}
		size := info.Size()
		if size > maxFileSize {
			warnings = append(warnings, fmt.Sprintf("file '%s' (%dKB) exceeded per-file limit %dKB, skipped", e.Name(), size/1024, maxFileSize/1024))
			continue
		}
		if total+size > maxTotalSize {
			warnings = append(warnings, fmt.Sprintf("file '%s' skipped (would exceed total limit %dKB)", e.Name(), maxTotalSize/1024))
			continue
		}
		data, err := os.ReadFile(filepath.Join(outputDir, e.Name()))
		if err != nil {
			warnings = append(warnings, fmt.Sprintf("read '%s': %v", e.Name(), err))
			continue
		}
		files = append(files, sandbox.Attachment{
			Name: e.Name(),
			B64:  base64.StdEncoding.EncodeToString(data),
		})
		total += size
	}
	return files, warnings
}

// newExecID 返回唯一执行 ID（16 hex 字符），用于隔离临时目录。
// 失败兜底走 timestamp（极不可能命中，避免 panic）。
func newExecID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}
