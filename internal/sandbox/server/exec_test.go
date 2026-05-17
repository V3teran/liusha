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
		TaskID:         "test-kill-pg",
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
	setupTestRoots(t)
	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	body, err := json.Marshal(sandbox.ExecRequest{
		TaskID:         "test-baseline",
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

// TestHandleExec_TaskIDValidation 验证 TaskID 必填 + path-safe 字符集（防 path traversal）。
func TestHandleExec_TaskIDValidation(t *testing.T) {
	setupTestRoots(t)
	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	cases := []struct {
		name   string
		taskID string
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
				TaskID:         c.taskID,
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
				t.Errorf("TaskID=%q expect 400 got %d", c.taskID, resp.StatusCode)
			}
		})
	}
}

// TestHandleExec_PerTaskIsolation 验证 2 个 task 写到 OUTPUT_DIR 的文件互不可见。
//
// task-a 写 a.txt；task-b 写 b.txt；task-a 再扫描时只看到 a.txt。
// 模拟 subtask swarm 父子并发场景的核心隔离不变量。
func TestHandleExec_PerTaskIsolation(t *testing.T) {
	setupTestRoots(t)
	srv := New()
	ts := httptest.NewServer(srv.mux)
	defer ts.Close()

	post := func(t *testing.T, taskID, cmd string) sandbox.ExecResult {
		t.Helper()
		body, _ := json.Marshal(sandbox.ExecRequest{
			TaskID:         taskID,
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
