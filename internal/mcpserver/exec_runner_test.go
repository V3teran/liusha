package mcpserver_test

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/mcpserver"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewExecRunner_Defaults(t *testing.T) {
	r := mcpserver.NewExecRunner()
	require.NotNil(t, r)
}

// TestExecRunner_CapturesStdoutStderr 验证 ExecRunner.Run 能分别收集
// 子进程的 stdout 和 stderr（并合并返回，stderr 带 [stderr] 前缀标注）。
func TestExecRunner_CapturesStdoutStderr(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 /bin/sh")
	}
	r := mcpserver.NewExecRunner()
	out, err := r.Run(context.Background(),
		[]string{"/bin/sh", "-c", "printf hello; printf err >&2"},
		5*time.Second,
	)
	require.NoError(t, err)
	assert.Contains(t, out, "hello")
	assert.Contains(t, out, "err")
}

// TestExecRunner_TimeoutKillsProcess 验证 timeout 到期会杀掉子进程，
// Run 在远早于 sleep 结束时返回非 nil 错误。
func TestExecRunner_TimeoutKillsProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("依赖 /bin/sleep")
	}
	r := mcpserver.NewExecRunner()
	start := time.Now()
	_, err := r.Run(context.Background(),
		[]string{"/bin/sleep", "5"},
		100*time.Millisecond,
	)
	elapsed := time.Since(start)
	require.Error(t, err)
	assert.Less(t, elapsed, 2*time.Second, "超时应在远小于 5s 内返回")
	assert.True(t,
		strings.Contains(err.Error(), "timeout") ||
			strings.Contains(err.Error(), "killed") ||
			strings.Contains(err.Error(), "signal") ||
			strings.Contains(err.Error(), "deadline"),
		"错误应体现超时/被杀：%v", err,
	)
}

// TestExecRunner_EmptyCommandErrors 验证空命令被拒绝。
func TestExecRunner_EmptyCommandErrors(t *testing.T) {
	r := mcpserver.NewExecRunner()
	_, err := r.Run(context.Background(), nil, time.Second)
	require.Error(t, err)
}
