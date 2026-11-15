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

// taskDirs 是一个任务的工作区目录（见 exec.go 开头的布局说明）。
type taskDirs struct {
	assignmentID, taskID string
	work, out, state     string
}

// ensureTaskDirs 建任务工作区的 work / out / state 目录。ID 须已校验为路径安全。
func (s *Server) ensureTaskDirs(assignmentID, taskID string) (taskDirs, error) {
	root := filepath.Join(s.cfg.WorkspaceDir, assignmentID, taskID)
	d := taskDirs{
		assignmentID: assignmentID, taskID: taskID,
		work: filepath.Join(root, "work"), out: filepath.Join(root, "out"), state: filepath.Join(root, "state"),
	}
	for _, dir := range []string{d.work, d.out, d.state} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return taskDirs{}, fmt.Errorf("mkdir %s: %w", dir, err)
		}
	}
	return d, nil
}

// buildExecCommand 构建命令：命令文本写入脚本文件交给 shell 执行、任务工作区与环境。
//
// 命令文本不经命令行传递：Windows 的命令行由目标程序自行切分，引号、反斜杠与通配的处理
// 因运行时而异，同一条命令到 shell 手里可能已被改写；脚本文件按字节原样交给 shell，全平台
// 同一语义。返回的 cleanup 须在命令结束后调用（停定时器、删脚本）。
func (s *Server) buildExecCommand(ctx context.Context, req *sandbox.ExecRequest, dirs taskDirs, outputDir string, timeout time.Duration) (*exec.Cmd, context.Context, func(), error) {
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
	cmd.Dir = dirs.work
	cmd.Env = append(s.baseEnv(), dirs.env(outputDir)...)
	cmd.Env = append(cmd.Env, "LIUSHA_AGENT_RUN_ID="+req.AgentRunID)
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

// env 是任务进程（命令与浏览器服务）的任务变量，outputDir 是本次的产物目录。任务标识也是
// 进程归属任务的依据（Linux 上按它识别任务进程）。目录以交给 agent 的形式给出
// （sandbox.AgentPath）；HOME 指向任务状态目录，工具按 $HOME 落盘的会话与配置随之归位到
// 任务。Windows 上不改写 USERPROFILE：系统组件与原生程序靠它定位真实的用户目录，改写会让
// 它们工作异常（实测浏览器因此无法启动）；需要按任务隔离的状态由各自显式指定（如浏览器
// 配置目录）。Windows 上 Python 写管道时缺省按 ANSI 代码页编码（非拉丁字符直接报错或乱码），
// 而输出按 UTF-8 回到 agent，故开启 UTF-8 模式——安全工具相当一部分是 Python 写的。
func (d taskDirs) env(outputDir string) []string {
	env := []string{
		"LIUSHA_ASSIGNMENT_ID=" + d.assignmentID,
		"LIUSHA_TASK_ID=" + d.taskID,
		"LIUSHA_WORKSPACE=" + sandbox.AgentPath(d.work),
		"LIUSHA_OUTPUT=" + sandbox.AgentPath(outputDir),
		"LIUSHA_STATE=" + sandbox.AgentPath(d.state),
		"HOME=" + sandbox.AgentPath(d.state),
		"OUTPUT_DIR=" + sandbox.AgentPath(outputDir),
	}
	if runtime.GOOS == "windows" {
		env = append(env, "PYTHONUTF8=1")
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
	if s.local.url != "" {
		// 本机接口：浏览器客户端经它启停浏览器服务，浏览器服务经它回灌流量。沙盒令牌不进入
		// 任何进程的环境。
		env = append(env, "LIUSHA_SANDBOX_LOCAL_URL="+s.local.url, "LIUSHA_SANDBOX_LOCAL_KEY="+s.local.key)
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
		name := envName(kv)
		if hasEnvPrefix(name, "LIUSHA_") || slices.ContainsFunc(secret, func(s string) bool { return sameEnvName(s, name) }) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

// prependPath 把 dir 加到 PATH 最前（无 PATH 时新建）。保留原变量名的写法（Windows 上通常是 Path）。
func prependPath(env []string, dir string) []string {
	for i, kv := range env {
		if name := envName(kv); sameEnvName(name, "PATH") {
			out := append([]string(nil), env...)
			out[i] = name + "=" + dir + string(os.PathListSeparator) + kv[len(name)+1:]
			return out
		}
	}
	return append(env, "PATH="+dir)
}

// envName 取 KEY=VALUE 中的变量名。Windows 环境块里有以 = 开头的驱动器当前目录项（=C:=C:\x），
// 名字从第二个 = 之前截取。
func envName(kv string) string {
	if i := strings.IndexByte(kv[min(1, len(kv)):], '='); i >= 0 {
		return kv[:i+min(1, len(kv))]
	}
	return kv
}

// sameEnvName 按平台规则比较变量名：Windows 不区分大小写。
func sameEnvName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func hasEnvPrefix(name, prefix string) bool {
	return len(name) >= len(prefix) && sameEnvName(name[:len(prefix)], prefix)
}

// executeCommand 运行命令并捕获输出。命令的进程归属到任务（taskProcesses：取消时结束这条
// 命令的进程，任务释放时结束任务的全部进程；机制按平台而定，见其定义）。
func (s *Server) executeCommand(cmdCtx context.Context, taskID string, cmd *exec.Cmd) (stdout, stderr string, exitCode int, timedOut bool) {
	var stdoutBuf, stderrBuf strings.Builder
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	runErr := s.procs.runGroup(taskID, cmd)

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
