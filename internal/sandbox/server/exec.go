package server

import (
	"context"
	"encoding/base64"
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

// outputDirRoot / workdirRoot 是 per-task 文件隔离的根目录。
//
// v1.3：单 task 1 容器，所有 /exec 共享 /tmp/sandbox-output（同 task 内跨 exec 复用文件）。
// v1.4 subtask swarm：orchestrator / exploitation 共享同一容器（避免账号 cookie 顶掉），但orchestrator / exploitation 并发跑命令会
// 互相串扰——modtime 过滤无法分清"orchestrator 刚写的 vs exploitation 刚写的"；wget -O ./x.html 类命令会互覆。
//
// 隔离设计：
//   - OUTPUT_DIR = /tmp/sandbox-output/<HunterID>/  → 附件按 task 切，collectAttachments 只扫本 task 子目录
//   - cwd        = /workspace/<HunterID>/           → LLM 写相对路径自动落到 per-task workdir
//   - 共享：home 目录（cookies / auth state）、二进制工具 — 这是orchestrator / exploitation 共享容器的目的
//
// task 容器销毁时整个目录树自然消失，无残留泄露风险。
// var（非 const）便于 server 包内单测用 t.TempDir() override：
// 单元测试在 host 跑，/workspace 等容器路径主机不存在/无权限 → mkdir 500。
// sandbox-server 单线程串行处理 /exec，无并发改 root 场景。
var (
	outputDirRoot = "/tmp/sandbox-output"
	workdirRoot   = "/workspace"
)

// handleExec 处理 POST /exec：sh -c 命令 + OUTPUT_DIR 附件机制。
//
// 流程：
//  1. 解析 ExecRequest，校验必填
//  2. ensure 共享目录 /tmp/sandbox-output 存在（容器内跨 exec 共享，注入为 $OUTPUT_DIR）
//  3. exec.CommandContext 跑 sh -c（带 timeout）
//  4. 扫描 output 目录 b64 编码 + 仅返本次 exec 新增/修改文件（modtime 过滤），命令崩溃也扫
//
// 并发安全：sandbox-server 是 per-agent-run 容器进程，本来就是单线程串行处理 /exec。
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	var req sandbox.ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode request: %v", err)
		return
	}
	if req.HunterID == "" {
		writeError(w, http.StatusBadRequest, "hunter_id required (subtask swarm 按 hunter 切目录隔离)")
		return
	}
	if !isPathSafeHunterID(req.HunterID) {
		writeError(w, http.StatusBadRequest, "hunter_id must be [A-Za-z0-9._-]{1,64}")
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

	// per-task 隔离：orchestrator / exploitation 共享容器但文件互不串扰（同 task 内跨 exec 仍共享 outputDir）
	outputDir := filepath.Join(outputDirRoot, req.HunterID)
	workdir := filepath.Join(workdirRoot, req.HunterID)
	if err := os.MkdirAll(outputDir, 0o777); err != nil {
		writeError(w, http.StatusInternalServerError, "create output dir: %v", err)
		return
	}
	if err := os.MkdirAll(workdir, 0o777); err != nil {
		writeError(w, http.StatusInternalServerError, "create workdir: %v", err)
		return
	}
	// 记录命令开始时间——collectAttachments 用此过滤"本次 exec 新增/修改"的文件
	// （per-task 隔离后仍需 modtime 过滤：同 task 多次 exec 旧文件不重复返）。
	// 减 1s 余量：很多 Linux 文件系统 mtime 是秒级粒度（写入瞬间的 mtime 被截断到整秒，
	// 可能落在纳秒精度的 now() 之前），不留余量会把本次刚写的文件误判为历史而丢掉
	// （macOS APFS 纳秒 mtime 不触发，故只在 Linux 复现）。代价仅是极偶发重复返同 task 1s 内旧文件，远轻于丢文件。
	execStart := time.Now().Add(-time.Second)

	// 命令超时控制——r.Context() 让客户端断开/取消能传到 sh 子进程
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	cmdCtx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", req.Command)
	cmd.Dir = workdir
	cmd.Env = append(os.Environ(),
		"OUTPUT_DIR="+outputDir,
		"HUNTER_ID="+req.HunterID, // browser-use wrapper（每 hunter 独立 tab）用此区分
	)

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

	// 扫描产物——命令崩溃也扫，半成品有诊断价值。
	// 仅返本次 exec 新增/修改文件（modtime > execStart），避免共享目录下历史文件重复返。
	files, warnings := collectAttachments(outputDir, execStart)
	res.Files = files
	res.Warnings = warnings

	writeJSON(w, http.StatusOK, res)
}

// collectAttachments 扫描 outputDir 顶层（不递归），返回 b64 编码的附件列表。
//
// since 过滤：仅返 modtime ≥ since 的文件（"本次 exec 新增/修改"），跳过历史文件。
// 共享目录方案下不加 modtime 过滤会让每次 /exec 都重复返之前所有图，撑爆 HTTP。
//
// 应用上限：
//   - 总大小 > maxTotalSize（10MB）：剩余文件全 warning
//   - 数量 > maxFileCount（5）：剩余文件全 warning
//   - 子目录：返回 warning（不递归扫描，避免 chrome cache 等噪音）
//
// LLM 想保留产物的命令把文件写到 $OUTPUT_DIR 顶层即可。
func collectAttachments(outputDir string, since time.Time) ([]sandbox.Attachment, []string) {
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
		// modtime 过滤：共享目录下历史文件（其它 exec 写的）跳过。
		// 用 .Before(since) 不用 < since：单调时钟避免边界 1ns 漂移。
		if info.ModTime().Before(since) {
			continue
		}
		size := info.Size()
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

// isPathSafeHunterID 校验 HunterID 是否仅含 path-safe 字符（[A-Za-z0-9._-]{1,64}）。
//
// 防 path traversal：req.HunterID 由 LLM 调用方注入 → server 端直接 filepath.Join
// 拼路径，若不校验可被 `../../etc/passwd` 类输入逃逸到 /workspace 根之外。
// 合法 agent_run id 是 uuid（36 字符含连字符），天然匹配本字符集。
func isPathSafeHunterID(s string) bool {
	if len(s) == 0 || len(s) > 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z':
		case c >= 'A' && c <= 'Z':
		case c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	return true
}
