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

// liushaRoot 是任务工作区的容器内根目录（宿主持久层经 launcher bind mount
// 挂到此路径）。
//
// 目录架构（task 一层隔离）：
//   - 两级分层：/root/liusha/workspace/<assignment_id>/<task_id>/
//   - work:  …/<task_id>/work/   → 命令执行的 cwd（scratch）
//   - out:   …/<task_id>/out/    → 附件输出（$OUTPUT_DIR）
//   - state: …/<task_id>/state/  → 会话与环境状态（任务内共享；
//     HOME 指向它，chromium 等按 $HOME 落盘的工具随之归位）
//
// 隔离设计：
//   - 任务间：目录即隔离边界（同 warm 容器多任务共存互不干扰）
//   - 任务内：多 agent 共享 work/out/state——共容器共目标，agent 级
//     目录是假隔离
//
// 真实持久化由 launcher 的 bind mount 提供（宿主 workspace 根目录挂到
// liushaRoot）：容器回收重建后任务工件仍在原宿主路径可恢复。
// var（非 const）便于 server 包内单测用 t.TempDir() override。
var liushaRoot = "/root/liusha/workspace"

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
//  4. 扫描 output 目录 b64 编码 + 仅返本次 exec 前不存在/后被修改的文件（目录快照
//     diff），命令崩溃也扫
//
// 并发：暖池容器由并发任务共享，net/http 每请求一个 goroutine 并发处理 /exec；处理流程
// 无进程内共享可变状态，工作区目录按 批次 / 任务 两级隔离。
func (s *HTTPServer) handleExec(w http.ResponseWriter, r *http.Request) {
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
	workspaceDir, outputDir, stateDir, ok := setupExecDirs(&req, w)
	if !ok {
		return
	}

	// 3. 构建执行命令
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	before := snapshotDir(outputDir)
	cmd, cmdCtx, cancel := buildExecCommand(r.Context(), &req, workspaceDir, outputDir, stateDir, timeout)
	defer cancel()

	// 4. 执行命令
	stdout, stderr, exitCode, timedOut := executeCommand(cmdCtx, cmd)

	// 5. 构建结果
	res := buildExecResult(stdout, stderr, exitCode, timedOut, outputDir, before)

	// 6. 返回结果
	writeJSON(w, http.StatusOK, res)
}

// snapshotDir 记录目录顶层的文件集合（name → modtime，名字是本次关心的全部——
// modtime 仅用于「命令改写了执行前已有的同名文件」这一情形的归属判断）。
func snapshotDir(dir string) map[string]time.Time {
	snap := map[string]time.Time{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return snap // 目录尚不存在 = 空集
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if info, err := e.Info(); err == nil {
			snap[e.Name()] = info.ModTime()
		}
	}
	return snap
}

// collectAttachments 扫描 outputDir 顶层（不递归），返回 b64 编码的附件列表。
//
// before 过滤：仅返「执行前不存在，或存在但 mtime 已变化」的文件（本次 exec
// 新增/改写），跳过历史文件。共享目录方案下不加过滤会让每次 /exec 都重复返
// 之前所有图，撑爆 HTTP。
//
// 弃用旧版 mtime ≥ execStart 过滤的原因：它拿命令开始时刻与文件系统 mtime 比
// 墙钟——毫秒内完成的命令在 mtime 精度粗于时钟差的文件系统上，刚写的文件会被
// 判成「历史文件」而丢失（CI 实测 Files=[]）。快照 diff 无时钟依赖，语义更严：
// 旧实现连「mtime 恰好相等的同秒旧文件」也会误收。
//
// 应用上限：
//   - 总大小 > maxTotalSize（10MB）：剩余文件全 warning
//   - 数量 > maxFileCount（5）：剩余文件全 warning
//   - 子目录：返回 warning（不递归扫描，避免 chrome cache 等噪音）
//
// LLM 想保留产物的命令把文件写到 $OUTPUT_DIR 顶层即可。
func collectAttachments(outputDir string, before map[string]time.Time) ([]sandbox.Attachment, []string) {
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
		// 快照 diff：执行前已存在且 mtime 未变的文件是历史文件，跳过。
		// 共享目录下其它 exec 写过的文件（不在本次命令的改动面）由此排除。
		if mt, existed := before[e.Name()]; existed && mt.Equal(info.ModTime()) {
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
