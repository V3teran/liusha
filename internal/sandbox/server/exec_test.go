package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// TestHandleExec_Timeout_KillsProcessGroup 是 e2e 实测踩过的 bug 的回归测试：
//
// 场景：sh -c "sleep 100 | tail" —— sleep 和 tail 都是 sh 的子进程，pipe 相连。
//
// 修复前：ctx 超时 → exec.CommandContext 只 SIGKILL sh 进程；sleep + tail 孤儿化
// 继续持有 stdout pipe writer end，cmd.Wait() 等待 stdio copying goroutine 退出，
// 直到 sleep 100 自然结束（100s）才返回 → 600s timeout 实际 wall time 100s+。
//
// 修复后：sh 是新进程组 leader（Setpgid），cmd.Cancel 杀整个进程组（负 PID 语义），
// sleep + tail 一起死，pipe 立即关闭，cmd.Wait() 立即返回。
//
// 验证：1s timeout 后 HTTP wall time < 3s（容忍 HTTP 往返 + defer 清理）+ TimedOut=true。
func TestHandleExec_Timeout_KillsProcessGroup(t *testing.T) {
	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	body, err := json.Marshal(sandbox.ExecRequest{
		Command:        "sleep 100 | tail",
		TimeoutSeconds: 1,
		Tag:            "kill-pg",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	start := time.Now()
	resp, err := http.Post(ts.URL+"/exec", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /exec: %v", err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d, want 200", resp.StatusCode)
	}

	var res sandbox.ExecResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}

	// 核心断言：wall time 必须远小于 sleep 100s（如果孤儿子进程 bug 重现会 ≈ 100s）
	if elapsed > 3*time.Second {
		t.Errorf("wall time %v > 3s: 进程组 SIGKILL 没杀全，孤儿子进程持有 pipe 导致 cmd.Wait 阻塞 (TimedOut=%v, ExitCode=%d)",
			elapsed, res.TimedOut, res.ExitCode)
	}
	if !res.TimedOut {
		t.Errorf("TimedOut=false（预期 true：1s timeout 应触发 cmd.Cancel）")
	}
}

// TestHandleExec_Normal_Succeeds 是基线 sanity check：
// 简单命令正常返回 stdout/stderr，不被 timeout 错误干掉。
func TestHandleExec_Normal_Succeeds(t *testing.T) {
	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	body, err := json.Marshal(sandbox.ExecRequest{
		Command:        "echo hello && echo err >&2",
		TimeoutSeconds: 5,
		Tag:            "baseline",
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	resp, err := http.Post(ts.URL+"/exec", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("POST /exec: %v", err)
	}
	defer resp.Body.Close()

	var res sandbox.ExecResult
	if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if res.ExitCode != 0 {
		t.Errorf("ExitCode=%d, want 0", res.ExitCode)
	}
	if res.TimedOut {
		t.Errorf("TimedOut=true，预期 false（命令瞬间返回）")
	}
	if res.Stdout != "hello\n" {
		t.Errorf("Stdout=%q, want 'hello\\n'", res.Stdout)
	}
	if res.Stderr != "err\n" {
		t.Errorf("Stderr=%q, want 'err\\n'", res.Stderr)
	}
}
