package server

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// liushaRoot 是 per-agent 文件隔离的根目录。
//
// v1.5 统一目录架构（task + agent 两层隔离）：
//   - 根目录：/liusha/<task_id>/<agent_id>/
//   - workspace: /liusha/<task_id>/<agent_id>/workspace/  → 命令执行的 cwd
//   - output:    /liusha/<task_id>/<agent_id>/output/     → 附件输出
//   - profile:   /liusha/<task_id>/profile/               → 浏览器、工具配置（Task 共享）
//
// 隔离设计：
//   - Task 级：同一 Task 的 Agent 共享 profile（浏览器登录态、cookies）
//   - Agent 级：每个 Agent 独立 workspace/output（避免并发文件冲突）
//   - 支持 planner + exploitation 并发执行，文件互不串扰
//
// 容器销毁时整个目录树自然消失，无残留泄露风险。
// var（非 const）便于 server 包内单测用 t.TempDir() override。
var liushaRoot = "/liusha"

// execWaitDelay 是 cmd.WaitDelay 的取值：进程退出 / ctx 取消起算，最多再等这么久就强制关
// I/O pipe 并让 cmd.Wait() 返回，兜底「子进程已退出但孤儿后台子进程仍持有 stdout pipe」类悬挂
// （详见 handleExec 内 cmd.WaitDelay 处注释）。var（非 const）便于单测注入小值。
var execWaitDelay = 10 * time.Second

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
	// 1. 解析并验证请求
	var req sandbox.ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode request: %v", err)
		return
	}
	if !validateExecRequest(&req, w) {
		return
	}

	// 2. 创建执行目录
	workspaceDir, outputDir, profileDir, ok := setupExecDirs(&req, w)
	if !ok {
		return
	}

	// 3. 构建执行命令
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	execStart := time.Now()
	cmd, cmdCtx := buildExecCommand(r.Context(), &req, workspaceDir, outputDir, profileDir, timeout)

	// 4. 执行命令
	stdout, stderr, exitCode, timedOut := executeCommand(cmd, cmdCtx)

	// 5. 构建结果
	res := buildExecResult(stdout, stderr, exitCode, timedOut, outputDir, execStart)

	// 6. 返回结果
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
		data, err := os.ReadFile(filepath.Join(outputDir, e.Name())) // #nosec G304 // 路径来自进程配置/种子目录，非用户输入
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

// isPathSafe 校验 ID 是否仅含 path-safe 字符（[A-Za-z0-9._-]{1,64}）。
//
// 防 path traversal：assignment_id / agent_id 由调用方注入 → server 端直接 filepath.Join
// 拼路径，若不校验可被 `../../etc/passwd` 类输入逃逸到 /liusha 根之外。
// 合法 UUID 是 36 字符含连字符，天然匹配本字符集。
func isPathSafe(s string) bool {
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
