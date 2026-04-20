package mcpserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"time"
)

// Runner 是工具执行抽象；供 tools 层在不关心底层（docker exec / 本地 exec）
// 的情况下统一调用。小接口（1 方法）便于测试替换。
type Runner interface {
	Run(ctx context.Context, cmd []string) (string, error)
}

// ExecRunner 在 mcp-server 进程所在的容器内直接 fork 子进程执行工具，
// 不依赖宿主机 docker.sock，从根上消除容器逃逸风险。
type ExecRunner struct{}

// NewExecRunner 创建 ExecRunner。
func NewExecRunner() *ExecRunner {
	return &ExecRunner{}
}

// Run 执行命令并捕获 stdout/stderr；timeout<=0 表示不强制超时。
// 返回 stdout+stderr 合并字符串（stderr 非空会附加 [stderr] 标注）。
func (r *ExecRunner) Run(ctx context.Context, cmd []string, timeout time.Duration) (string, error) {
	if len(cmd) == 0 {
		return "", errors.New("命令不能为空")
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var stdout, stderr bytes.Buffer
	c := exec.CommandContext(ctx, cmd[0], cmd[1:]...) //nolint:gosec // 命令白名单由上游 tools 定义
	c.Stdout = &stdout
	c.Stderr = &stderr
	err := c.Run()
	out := stdout.String()
	if stderr.Len() > 0 {
		if out != "" {
			out += "\n"
		}
		out += "[stderr]\n" + stderr.String()
	}
	if err != nil {
		// ctx 超时时优先返回 deadline 错误，便于调用方判定。
		if ctxErr := ctx.Err(); ctxErr != nil {
			return out, fmt.Errorf("命令超时或被取消: %w", ctxErr)
		}
		return out, fmt.Errorf("执行命令 %v 失败: %w", cmd, err)
	}
	return out, nil
}

// AsRunner 返回一个固定超时的 Runner 适配器，满足 Runner 接口。
func (r *ExecRunner) AsRunner(timeout time.Duration) Runner {
	return &execRunnerAdapter{inner: r, timeout: timeout}
}

type execRunnerAdapter struct {
	inner   *ExecRunner
	timeout time.Duration
}

func (a *execRunnerAdapter) Run(ctx context.Context, cmd []string) (string, error) {
	return a.inner.Run(ctx, cmd, a.timeout)
}
