package server

import (
	"encoding/json"
	"net/http"
	"os"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// 任务工作区目录（任务一层隔离）：
//
//	<工作区根>/<批次>/<任务>/out    本任务的文件面：命令的产物（$OUTPUT_DIR 的落点），与超过
//	                                输出落盘阈值的完整输出留档（<时间戳>.stdout/.stderr，由收集器
//	                                直接写在这里，见 exec_capture.go）
//	<工作区根>/<批次>/<任务>/state  会话与环境状态（HOME 指向它，chromium 等按 $HOME 落盘的工具随之归位）
//
// 命令的 cwd 与 $OUTPUT_DIR 是同一个目录：本次执行的暂存目录（工作区根下 .exec/out-XXX，见
// execOutput），其中预置 out 既有顶层文件的硬链接——相对路径写盘与新产物一样被收编进 out 并
// 归属本次执行，旧产物照常可读，删掉暂存里的名字不动 out 里的真身；执行结束暂存目录删除。
// 任务间以目录为隔离边界；任务内多 agent 共享 out——同任务共目标，agent 级目录是假隔离。
// 工作区根与 api 的文件面是同一目录，工件因此在沙盒重建后仍在。

// execWaitDelay 是 cmd.WaitDelay：进程退出 / ctx 取消起算，最多再等这么久就强制关 I/O 管道并让
// cmd.Wait() 返回，兜底「命令已退出但后台子进程仍持有 stdout 管道」的悬挂。var 便于单测注入小值。
var execWaitDelay = 10 * time.Second

// handleExec 处理 POST /exec：
//  1. 解析并校验请求
//  2. 建任务工作区目录（work / out / state）
//  3. 本次执行的 $OUTPUT_DIR 是专属暂存目录，也是命令的 cwd（见 execOutput）：结束后产物移入
//     out/ 并作为附件返回
//  4. 命令的进程归属于任务：超时 / 请求断开结束这条命令的全部进程；正常结束后留在
//     后台的进程（监听器等）保留到任务释放
//  5. 输出有界收集：越过请求的落盘阈值就完整落盘，响应带完整字节数与落盘路径（见 exec_capture.go）
//
// 并发：并发任务的请求各占一个 goroutine；工作区按 批次 / 任务 两级隔离。
func (s *Server) handleExec(w http.ResponseWriter, r *http.Request) {
	var req sandbox.ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "decode request: %v", err)
		return
	}
	if !validateExecRequest(&req, w) {
		return
	}

	dirs, err := s.ensureTaskDirs(req.AssignmentID, req.TaskID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	staged, err := stageOutput(s.cfg.WorkspaceDir, dirs.out)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	cmd, cmdCtx, cleanup, err := s.buildExecCommand(r.Context(), &req, dirs, staged.dir, timeout)
	if err != nil {
		staged.discard()
		writeError(w, http.StatusInternalServerError, "%v", err)
		return
	}
	defer cleanup()
	capture := newOutputCapture(req.SaveOutputOverBytes, dirs.out)
	streams, exitCode, timedOut := s.executeCommand(cmdCtx, req.TaskID, cmd, capture)

	res := sandbox.ExecResult{
		ExitCode: exitCode, TimedOut: timedOut,
		Stdout: streams.stdout, Stderr: streams.stderr,
		StdoutBytes: streams.stdoutBytes, StderrBytes: streams.stderrBytes,
		StdoutFile: streams.stdoutFile, StderrFile: streams.stderrFile,
		Warnings: streamWarnings(streams),
	}
	files, warnings := staged.publish()
	res.Files, res.Warnings = files, append(res.Warnings, warnings...)
	writeJSON(w, http.StatusOK, res)
}

// snapshotDir 记录目录顶层的文件集合（name → modtime；modtime 仅用于「命令改写了执行前
// 已有的同名文件」这一情形的归属判断）。
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

// isPathSafe 校验 ID 只含路径安全字符（[A-Za-z0-9._-]{1,64}，且不是 . / ..）：ID 直接拼进
// 工作区路径与浏览器套接字名，不校验可被 `../../etc` 类输入逃逸。UUID 天然满足。
func isPathSafe(s string) bool {
	if len(s) == 0 || len(s) > 64 || s == "." || s == ".." {
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
