package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// setupTestRoots 把 outputDirRoot / workdirRoot 临时换成 t.TempDir() 子目录，
// 让 handleExec mkdir 不撞 host /workspace 权限。Cleanup 自动还原。
func setupTestRoots(t *testing.T) {
	t.Helper()
	oldOut, oldWork := outputDirRoot, workdirRoot
	tmp := t.TempDir()
	outputDirRoot = filepath.Join(tmp, "output")
	workdirRoot = filepath.Join(tmp, "workspace")
	t.Cleanup(func() {
		outputDirRoot, workdirRoot = oldOut, oldWork
	})
}

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
	setupTestRoots(t)
	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	body, err := json.Marshal(sandbox.ExecRequest{
		ExecutorID:       "test-kill-pg",
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

// TestHandleExec_BackgroundOrphan_DoesNotHangPastWaitDelay 复现真实扫描 abort 根因：
//
// 场景：LLM 跑 RFI 测试 HTTP 服务器（如 `python3 -m http.server &`）——命令把长命子进程放后台，
// sh 立即退出（exit 0），但孤儿子进程继续持有 stdout pipe writer end。cmd.Wait() 会一直等
// stdio copy goroutine 退出（即子进程自然结束）才返回。真实场景 http.server 永不退 →
// handleExec 挂到 client 31min timeout（"Client.Timeout exceeded while awaiting headers"）→
// run_command 报错 → 整个 active run abort（logs/runner.local.log 实测 tag=test-rfi-http-server）。
//
// 仅靠进程组 SIGKILL（TestHandleExec_Timeout_KillsProcessGroup）救不了本例：子进程 `&` 后台化
// 且这里 timeout 远未到、根本不触发 cmd.Cancel。修复靠 cmd.WaitDelay：进程退出/ctx 取消起算，
// 最多再等 WaitDelay 就强制关 pipe 并让 Wait 返回，不再傻等孤儿子进程。
//
// 验证：子进程 sleep 30s，但 timeout 给 60s（不触发 cmdCtx），handleExec 必须在 WaitDelay 量级返回。
func TestHandleExec_BackgroundOrphan_DoesNotHangPastWaitDelay(t *testing.T) {
	setupTestRoots(t)
	oldDelay := execWaitDelay
	execWaitDelay = 500 * time.Millisecond
	t.Cleanup(func() { execWaitDelay = oldDelay })

	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	// `sleep 30 &`：sh 后台启子进程后立即退出，但 sleep 持有 stdout pipe 30s。
	// timeout 60s 远大于 sleep → cmdCtx 不触发，纯靠 WaitDelay 兜底。
	body, err := json.Marshal(sandbox.ExecRequest{
		ExecutorID:       "test-bg-orphan",
		Command:        "sleep 30 &",
		TimeoutSeconds: 60,
		Tag:            "bg-orphan",
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

	// 核心断言：必须在 WaitDelay 量级返回（容忍 HTTP 往返），绝不能等到 sleep 30s 自然结束。
	// 没修复时会等到 ≈30s（真实场景挂到 client 31min timeout → 整个 run abort）。
	if elapsed > 5*time.Second {
		t.Errorf("wall time %v：后台孤儿子进程持有 pipe 致 cmd.Wait 阻塞，WaitDelay 未生效（真实场景会挂到 client 31min timeout→abort）", elapsed)
	}
}

// TestHandleExec_Normal_Succeeds 是基线 sanity check：
// 简单命令正常返回 stdout/stderr，不被 timeout 错误干掉。
func TestHandleExec_Normal_Succeeds(t *testing.T) {
	setupTestRoots(t)
	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	body, err := json.Marshal(sandbox.ExecRequest{
		ExecutorID:       "test-baseline",
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

// TestHandleExec_ExecutorIDValidation 验证 ExecutorID 必填 + path-safe 字符集（防 path traversal）。
func TestHandleExec_ExecutorIDValidation(t *testing.T) {
	setupTestRoots(t)
	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	cases := []struct {
		name     string
		agentID string
	}{
		{"empty", ""},
		{"path traversal", "../etc"},
		{"slash", "a/b"},
		{"space", "task one"},
		{"unicode", "task中文"},
		{"too long", string(make([]byte, 65))},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			body, _ := json.Marshal(sandbox.ExecRequest{
				ExecutorID:       c.agentID,
				Command:        "echo x",
				TimeoutSeconds: 5,
				Tag:            "validate",
			})
			resp, err := http.Post(ts.URL+"/exec", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatalf("POST: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusBadRequest {
				t.Errorf("ExecutorID=%q expect 400 got %d", c.agentID, resp.StatusCode)
			}
		})
	}
}

// TestHandleExec_PerTaskIsolation 验证 2 个 task 写到 OUTPUT_DIR 的文件互不可见。
//
// task-a 写 a.txt；task-b 写 b.txt；task-a 再扫描时只看到 a.txt。
// 模拟 subtask swarm planner / exploitation 并发场景的核心隔离不变量。
func TestHandleExec_PerTaskIsolation(t *testing.T) {
	setupTestRoots(t)
	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	post := func(t *testing.T, agentID, cmd string) sandbox.ExecResult {
		t.Helper()
		body, _ := json.Marshal(sandbox.ExecRequest{
			ExecutorID:       agentID,
			Command:        cmd,
			TimeoutSeconds: 5,
			Tag:            "isolation",
		})
		resp, err := http.Post(ts.URL+"/exec", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("POST: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("status %d, want 200", resp.StatusCode)
		}
		var res sandbox.ExecResult
		if err := json.NewDecoder(resp.Body).Decode(&res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return res
	}

	// task-a 写 a.txt
	resA1 := post(t, "task-a", `printf 'A\n' > "$OUTPUT_DIR/a.txt"`)
	if len(resA1.Files) != 1 || resA1.Files[0].Name != "a.txt" {
		t.Fatalf("task-a 应只看到 a.txt，got %+v", resA1.Files)
	}

	// task-b 写 b.txt — 不可看到 task-a 的 a.txt
	resB := post(t, "task-b", `printf 'B\n' > "$OUTPUT_DIR/b.txt"`)
	if len(resB.Files) != 1 || resB.Files[0].Name != "b.txt" {
		t.Fatalf("task-b 应只看到 b.txt（不应捞到 task-a 的 a.txt），got %+v", resB.Files)
	}

	// task-a 第 2 次 exec ls — 仍只看到自己 a.txt
	resA2 := post(t, "task-a", `ls "$OUTPUT_DIR"`)
	if !bytes.Contains([]byte(resA2.Stdout), []byte("a.txt")) {
		t.Errorf("task-a 看不到自己的 a.txt，stdout=%q", resA2.Stdout)
	}
	if bytes.Contains([]byte(resA2.Stdout), []byte("b.txt")) {
		t.Errorf("task-a 捞到了 task-b 的 b.txt，隔离失败，stdout=%q", resA2.Stdout)
	}

	// cwd 隔离：task-a 写 ./relative.txt，task-b 看不到
	resA3 := post(t, "task-a", `printf 'A-cwd\n' > relative.txt && ls`)
	if !bytes.Contains([]byte(resA3.Stdout), []byte("relative.txt")) {
		t.Errorf("task-a cwd 写文件失败，stdout=%q", resA3.Stdout)
	}
	resB2 := post(t, "task-b", `ls`)
	if bytes.Contains([]byte(resB2.Stdout), []byte("relative.txt")) {
		t.Errorf("task-b cwd 看到了 task-a 的 relative.txt，隔离失败，stdout=%q", resB2.Stdout)
	}
}
