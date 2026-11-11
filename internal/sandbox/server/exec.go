package server

import (
	"encoding/json"
	"net/http"
	"os"
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
//  2. 建任务工作区目录（work / out / state，按 批次 / 任务 两级）
//  3. exec.CommandContext 跑 sh -c（带 timeout）
//  4. 本次执行的 $OUTPUT_DIR 是专属暂存目录（见 execOutput），结束后产物移入 out/
//     并作为附件返回，命令崩溃也收
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

	// 3. 本次执行专属的产物暂存目录（系统操作不收集产物）
	var out *execOutput
	cmdOutputDir := outputDir
	if !isSystemExec(&req) {
		staged, err := stageOutput(outputDir)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "%v", err)
			return
		}
		out, cmdOutputDir = staged, staged.dir
	}

	// 4. 执行命令
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	cmd, cmdCtx, cancel := buildExecCommand(r.Context(), &req, workspaceDir, cmdOutputDir, stateDir, timeout)
	defer cancel()
	stdout, stderr, exitCode, timedOut := executeCommand(cmdCtx, cmd)

	// 5. 产物移入 out/，构建结果
	res := sandbox.ExecResult{Stdout: stdout, Stderr: stderr, ExitCode: exitCode, TimedOut: timedOut}
	if out != nil {
		res.Files, res.Warnings = out.publish()
	}
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
