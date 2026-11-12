package server

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
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

// buildExecCommand 构建命令：命令文本写入脚本文件交给 shell 执行、任务工作区与环境。
//
// 命令文本不经命令行传递：Windows 的命令行由目标程序自行切分，引号、反斜杠与通配的处理
// 因运行时而异，同一条命令到 shell 手里可能已被改写；脚本文件按字节原样交给 shell，全平台
// 同一语义。返回的 cleanup 须在命令结束后调用（停定时器、删脚本）。
func (s *Server) buildExecCommand(ctx context.Context, req *sandbox.ExecRequest, workDir, outputDir, stateDir string, timeout time.Duration) (*exec.Cmd, context.Context, func(), error) {
	script, err := writeScript(s.cfg.WorkspaceDir, req.Command)
	if err != nil {
		return nil, nil, nil, err
	}
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	cleanup := func() {
		cancel()
		_ = os.Remove(script)
	}

	cmd := exec.CommandContext(cmdCtx, s.cfg.Shell, sandbox.AgentPath(script)) // #nosec G204 // 沙盒的职责就是执行 agent 下发的命令
	cmd.Dir = workDir
	cmd.Env = append(s.baseEnv(), taskEnv(req, workDir, outputDir, stateDir)...)
	cmd.WaitDelay = execWaitDelay
	return cmd, cmdCtx, cleanup, nil
}

// writeScript 把命令文本写进工作区根下 .exec/ 的一次性脚本文件（顶层点目录不进工作区文件面）。
func writeScript(root, command string) (string, error) {
	dir := filepath.Join(root, ".exec")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", dir, err)
	}
	f, err := os.CreateTemp(dir, "cmd-*.sh")
	if err != nil {
		return "", fmt.Errorf("建命令脚本: %w", err)
	}
	if _, err := f.WriteString(command); err != nil {
		_ = f.Close()
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("写命令脚本: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("写命令脚本: %w", err)
	}
	return f.Name(), nil
}

// taskEnv 是一条命令的任务变量。目录以交给 agent 的形式给出（sandbox.AgentPath）；HOME 指向
// 任务状态目录，工具按 $HOME 落盘的会话与配置随之归位到任务。Windows 原生程序按
// USERPROFILE 而不是 HOME 定位用户目录，同样指向它。
func taskEnv(req *sandbox.ExecRequest, workDir, outputDir, stateDir string) []string {
	env := []string{
		"LIUSHA_TASK_ID=" + req.TaskID,
		"LIUSHA_AGENT_RUN_ID=" + req.AgentRunID,
		"LIUSHA_WORKSPACE=" + sandbox.AgentPath(workDir),
		"LIUSHA_OUTPUT=" + sandbox.AgentPath(outputDir),
		"LIUSHA_STATE=" + sandbox.AgentPath(stateDir),
		"HOME=" + sandbox.AgentPath(stateDir),
		"OUTPUT_DIR=" + sandbox.AgentPath(outputDir),
	}
	if runtime.GOOS == "windows" {
		env = append(env, "USERPROFILE="+stateDir)
	}
	return env
}

// baseEnv 是每条命令共有的环境：沙盒进程环境去掉 liusha 的密钥，加上浏览器能力所需的变量。
func (s *Server) baseEnv() []string {
	env := scrubEnv(os.Environ(), s.cfg.SecretEnv)
	if s.cfg.BrowserBin != "" {
		env = prependPath(env, s.cfg.BrowserBin)
	}
	if s.cfg.BrowserDir != "" {
		env = append(env, "LIUSHA_BROWSER_DIR="+sandbox.AgentPath(s.cfg.BrowserDir))
	}
	if s.relay.url != "" {
		// 浏览器服务经回环中继回灌，由沙盒带令牌转发：沙盒令牌不进入任何命令的环境。
		env = append(env, "LIUSHA_INGEST_RELAY_URL="+s.relay.url, "LIUSHA_INGEST_RELAY_KEY="+s.relay.key)
	}
	if s.browsersPath != "" {
		env = append(env, "PLAYWRIGHT_BROWSERS_PATH="+s.browsersPath)
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

// executeCommand 运行命令并捕获输出。命令的进程归属到任务（taskProcesses：取消时结束这条
// 命令的进程，任务释放时结束任务的全部进程；机制按平台而定，见其定义）。
func (s *Server) executeCommand(cmdCtx context.Context, taskID string, cmd *exec.Cmd) (stdout, stderr string, exitCode int, timedOut bool) {
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := s.procs.run(taskID, cmd)

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
