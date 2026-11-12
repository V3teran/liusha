package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// validateExecRequest 校验 /exec 请求，失败时写 400 并返回 false。
func validateExecRequest(req *sandbox.ExecRequest, w http.ResponseWriter) bool {
	for _, f := range []struct{ name, value string }{
		{"assignment_id", req.AssignmentID},
		{"task_id", req.TaskID},
		{"agent_run_id", req.AgentRunID},
	} {
		if !isPathSafe(f.value) {
			writeError(w, http.StatusBadRequest, "%s must be [A-Za-z0-9._-]{1,64}", f.name)
			return false
		}
	}
	if req.Command == "" {
		writeError(w, http.StatusBadRequest, "command required")
		return false
	}
	if req.TimeoutSeconds <= 0 {
		writeError(w, http.StatusBadRequest, "timeout_seconds must be > 0")
		return false
	}
	return true
}

// setupExecDirs 建任务工作区的 work / out / state 目录。
func (s *Server) setupExecDirs(req *sandbox.ExecRequest, w http.ResponseWriter) (workDir, outDir, stateDir string, ok bool) {
	taskRoot := filepath.Join(s.cfg.WorkspaceDir, req.AssignmentID, req.TaskID)
	workDir = filepath.Join(taskRoot, "work")
	outDir = filepath.Join(taskRoot, "out")
	stateDir = filepath.Join(taskRoot, "state")
	for _, dir := range []string{workDir, outDir, stateDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			writeError(w, http.StatusInternalServerError, "mkdir %s: %v", dir, err)
			return "", "", "", false
		}
	}
	return workDir, outDir, stateDir, true
}

// buildExecCommand 构建命令：sh -c、独立进程组（取消即杀整组）、任务工作区与环境。
// 返回的 cancel 须由调用方在命令结束后调用，否则定时器泄漏到请求生命周期之外。
func (s *Server) buildExecCommand(ctx context.Context, req *sandbox.ExecRequest, workDir, outputDir, stateDir string, timeout time.Duration) (*exec.Cmd, context.Context, context.CancelFunc) {
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)

	cmd := exec.CommandContext(cmdCtx, "sh", "-c", req.Command) // #nosec G204 // 沙盒的职责就是执行 agent 下发的命令
	cmd.Dir = workDir
	cmd.Env = append(s.baseEnv(),
		"LIUSHA_TASK_ID="+req.TaskID,
		"LIUSHA_AGENT_RUN_ID="+req.AgentRunID,
		"LIUSHA_WORKSPACE="+workDir,
		"LIUSHA_OUTPUT="+outputDir,
		"LIUSHA_STATE="+stateDir,
		"HOME="+stateDir,
		"OUTPUT_DIR="+outputDir,
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = execWaitDelay
	return cmd, cmdCtx, cancel
}

// baseEnv 是每条命令共有的环境：沙盒进程环境去掉 liusha 的密钥，加上浏览器能力所需的变量。
func (s *Server) baseEnv() []string {
	env := scrubEnv(os.Environ(), s.cfg.SecretEnv)
	if s.cfg.BrowserBin != "" {
		env = prependPath(env, s.cfg.BrowserBin)
	}
	if s.cfg.IngestSocket != "" {
		// 浏览器服务经本地套接字回灌，由沙盒带令牌转发：令牌不进入任何命令的环境。
		env = append(env, "LIUSHA_INGEST_SOCKET="+s.cfg.IngestSocket)
	}
	if s.cfg.ViewportWidth > 0 && s.cfg.ViewportHeight > 0 {
		env = append(env,
			"LIUSHA_VIEWPORT_WIDTH="+strconv.Itoa(s.cfg.ViewportWidth),
			"LIUSHA_VIEWPORT_HEIGHT="+strconv.Itoa(s.cfg.ViewportHeight))
	}
	return env
}

// scrubEnv 去掉不该进入 agent 命令的变量：命令由 LLM 编写、输出回到 LLM，单进程部署（serve）
// 时沙盒与 api / runner 共享进程环境，其中有数据库连接串、密钥加密密钥、LLM 密钥等。
// 去掉全部 LIUSHA_ 变量（命令所需的由沙盒按需重新注入）与 secret 中列出的变量；其余原样保留
// （用户有意提供给工具的凭据，如侦察工具的 API 密钥）。
func scrubEnv(env, secret []string) []string {
	out := make([]string, 0, len(env))
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if strings.HasPrefix(name, "LIUSHA_") || slices.Contains(secret, name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// prependPath 把 dir 加到 PATH 最前（无 PATH 时新建）。
func prependPath(env []string, dir string) []string {
	for i, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			out := append([]string(nil), env...)
			out[i] = "PATH=" + dir + string(os.PathListSeparator) + v
			return out
		}
	}
	return append(env, "PATH="+dir)
}

// executeCommand 运行命令并捕获输出。命令的进程组交给 taskProcesses（任务释放时据以结束，
// 归属识别方式按平台而定，见其定义）。
func (s *Server) executeCommand(cmdCtx context.Context, taskID string, cmd *exec.Cmd) (stdout, stderr string, exitCode int, timedOut bool) {
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := cmd.Start()
	if runErr == nil {
		pgid := cmd.Process.Pid // Setpgid：进程组号即组长 pid
		s.procs.add(taskID, pgid)
		runErr = cmd.Wait()
		s.procs.forgetIfGone(taskID, pgid)
	}

	stdout, stderr = stdoutBuf.String(), stderrBuf.String()
	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
			if stderr == "" {
				stderr = runErr.Error()
			}
		}
	}
	timedOut = errors.Is(cmdCtx.Err(), context.DeadlineExceeded)
	return stdout, stderr, exitCode, timedOut
}
