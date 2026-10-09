package server

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// validateExecRequest 验证 Exec 请求
func validateExecRequest(req *sandbox.ExecRequest, w http.ResponseWriter) bool {
	if req.TaskID == "" {
		writeError(w, http.StatusBadRequest, "task_id required")
		return false
	}
	if req.AgentID == "" {
		writeError(w, http.StatusBadRequest, "agent_id required")
		return false
	}
	if !isPathSafe(req.TaskID) {
		writeError(w, http.StatusBadRequest, "task_id must be [A-Za-z0-9._-]{1,64}")
		return false
	}
	if !isPathSafe(req.AgentID) {
		writeError(w, http.StatusBadRequest, "agent_id must be [A-Za-z0-9._-]{1,64}")
		return false
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

// setupExecDirs 创建执行目录结构
func setupExecDirs(req *sandbox.ExecRequest, w http.ResponseWriter) (workspaceDir, outputDir, profileDir string, ok bool) {
	// task + agent 两层隔离
	taskRoot := filepath.Join(liushaRoot, req.TaskID)
	agentRoot := filepath.Join(taskRoot, req.AgentID)
	workspaceDir = filepath.Join(agentRoot, "workspace")
	outputDir = filepath.Join(agentRoot, "output")
	profileDir = filepath.Join(taskRoot, "profile") // Task 共享

	// 创建所有必要目录
	for _, dir := range []string{workspaceDir, outputDir, profileDir} {
		if err := os.MkdirAll(dir, 0o750); err != nil {
			writeError(w, http.StatusInternalServerError, "mkdir %s: %v", dir, err)
			return "", "", "", false
		}
	}

	return workspaceDir, outputDir, profileDir, true
}

// buildExecCommand 构建执行命令
func buildExecCommand(ctx context.Context, req *sandbox.ExecRequest, workspaceDir, outputDir, profileDir string, timeout time.Duration) (*exec.Cmd, context.Context) {
	cmdCtx, _ := context.WithTimeout(ctx, timeout)
	
	cmd := exec.CommandContext(cmdCtx, "sh", "-c", req.Command) // #nosec G204
	cmd.Dir = workspaceDir
	cmd.Env = append(os.Environ(),
		"LIUSHA_TASK_ID="+req.TaskID,
		"LIUSHA_AGENT_ID="+req.AgentID,
		"LIUSHA_WORKSPACE="+workspaceDir,
		"LIUSHA_OUTPUT="+outputDir,
		"LIUSHA_PROFILE="+profileDir,
		"HOME="+profileDir,
		"OUTPUT_DIR="+outputDir,
	)

	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = execWaitDelay

	return cmd, cmdCtx
}

// executeCommand 执行命令并捕获输出
func executeCommand(cmd *exec.Cmd, cmdCtx context.Context) (stdout, stderr string, exitCode int, timedOut bool) {
	var stdoutBuilder, stderrBuilder strings.Builder
	cmd.Stdout = &stdoutBuilder
	cmd.Stderr = &stderrBuilder

	runErr := cmd.Run()

	stdout = stdoutBuilder.String()
	stderr = stderrBuilder.String()

	if runErr != nil {
		var ee *exec.ExitError
		if errors.As(runErr, &ee) {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}

	timedOut = errors.Is(cmdCtx.Err(), context.DeadlineExceeded)

	return
}

// buildExecResult 构建执行结果
func buildExecResult(stdout, stderr string, exitCode int, timedOut bool, outputDir string, execStart time.Time) sandbox.ExecResult {
	res := sandbox.ExecResult{
		Stdout:   stdout,
		Stderr:   stderr,
		ExitCode: exitCode,
		TimedOut: timedOut,
	}

	files, warnings := collectAttachments(outputDir, execStart)
	res.Files = files
	res.Warnings = warnings

	return res
}
