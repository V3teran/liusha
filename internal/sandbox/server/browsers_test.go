package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/sandbox"
)

// fakeServiceScript 模拟浏览器服务：写端点（含自己的进程号）后常驻。
const fakeServiceScript = `import json, os, sys, time
identity, d = sys.argv[1], sys.argv[2]
with open(os.path.join(d, identity + ".json"), "w") as f:
    json.dump({"port": 1, "key": "k", "pid": os.getpid(), "task": os.environ.get("LIUSHA_TASK_ID")}, f)
time.sleep(300)
`

// abruptServiceScript 模拟收尾前就被结束的浏览器服务：真实监听、应答 reset 后立即退出，
// 不删除自己的端点（正常的服务会删，被强制结束时来不及）。
const abruptServiceScript = `import json, os, socket, sys
identity, d = sys.argv[1], sys.argv[2]
srv = socket.socket()
srv.bind(("127.0.0.1", 0))
srv.listen()
with open(os.path.join(d, identity + ".json"), "w") as f:
    json.dump({"port": srv.getsockname()[1], "key": "k", "pid": os.getpid()}, f)
conn, _ = srv.accept()
conn.recv(4096)
conn.sendall(b'{"stdout":"ok\\n","stderr":"","code":0}')
conn.close()
os._exit(0)
`

// browserHarness 起一个配了浏览器能力的沙盒（服务脚本是 fakeServiceScript）。
func browserHarness(t *testing.T) (*harness, string, string) {
	t.Helper()
	return browserHarnessWith(t, fakeServiceScript)
}

// browserHarnessWith 起一个配了浏览器能力的沙盒，服务脚本是 script。
func browserHarnessWith(t *testing.T, script string) (*harness, string, string) {
	t.Helper()
	python := ""
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			python = p
			break
		}
	}
	if python == "" {
		t.Skip("本机没有 Python")
	}
	scripts, browserDir := t.TempDir(), t.TempDir()
	service := filepath.Join(scripts, "service.py")
	if err := os.WriteFile(service, []byte(script), 0o600); err != nil {
		t.Fatal(err)
	}
	h, _ := liveServer(t, func(c *Config) {
		c.BrowserPython, c.BrowserService, c.BrowserDir = python, service, browserDir
	})
	localURL := strings.TrimSpace(execEnv(t, h, `printf '%s' "$LIUSHA_SANDBOX_LOCAL_URL"`))
	localKey := strings.TrimSpace(execEnv(t, h, `printf '%s' "$LIUSHA_SANDBOX_LOCAL_KEY"`))
	return h, browserDir, localURL + " " + localKey
}

func localCall(t *testing.T, local, method, path, body string) int {
	t.Helper()
	code, err := localDo(local, method, path, body)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// localDo 调用本机接口（可在测试协程之外使用）。
func localDo(local, method, path, body string) (int, error) {
	url, key, _ := strings.Cut(local, " ")
	req, _ := http.NewRequest(method, url+path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	_ = resp.Body.Close()
	return resp.StatusCode, nil
}

// waitEndpoint 等任务的某身份的服务写出端点，返回其中记录的进程号与任务标识。
func waitEndpoint(t *testing.T, dir, taskID, identity string) (int, string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if raw, err := os.ReadFile(filepath.Join(dir, taskID, identity+".json")); err == nil {
			var ep struct {
				PID  int    `json:"pid"`
				Task string `json:"task"`
			}
			if json.Unmarshal(raw, &ep) == nil && ep.PID > 0 {
				return ep.PID, ep.Task
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("服务未写出端点")
	return 0, ""
}

const startBody = `{"assignment_id":"asg","task_id":"task-a","identity":"default"}`

// 浏览器服务由沙盒启动、归属任务：重复启动请求不起第二个；启动它的命令超时被结束时它不受
// 牵连；强制结束只结束它；任务释放带走它。
func TestBrowsers_ServiceOwnedByTaskNotByCommand(t *testing.T) {
	h, dir, local := browserHarness(t)

	if code := localCall(t, local, http.MethodPost, "/browsers", startBody); code != http.StatusNoContent {
		t.Fatalf("启动请求应成功, got %d", code)
	}
	pid, task := waitEndpoint(t, dir, "task-a", "default")
	t.Cleanup(func() { killProcess(pid) })
	if task != "task-a" {
		t.Fatalf("服务应带任务环境, got %q", task)
	}
	if code := localCall(t, local, http.MethodPost, "/browsers", startBody); code != http.StatusNoContent {
		t.Fatalf("重复启动请求应成功, got %d", code)
	}
	if again, _ := waitEndpoint(t, dir, "task-a", "default"); again != pid {
		t.Fatalf("服务在运行时不应再启动一个: %d → %d", pid, again)
	}

	// 一条超时被结束的命令不牵连服务。
	body, _ := json.Marshal(sandbox.ExecRequest{AssignmentID: "asg", TaskID: "task-a", AgentRunID: "r", Command: "sleep 30", TimeoutSeconds: 1})
	var res sandbox.ExecResult
	h.decode(t, http.MethodPost, "/exec", bytes.NewReader(body), &res)
	if !res.TimedOut || !processAlive(pid) {
		t.Fatalf("命令超时不应结束浏览器服务: timedOut=%v alive=%v", res.TimedOut, processAlive(pid))
	}

	// 强制结束：只结束这个服务，删除端点。
	if code := localCall(t, local, http.MethodDelete, "/browsers/default?task_id=task-a", ""); code != http.StatusNoContent {
		t.Fatalf("结束请求应成功, got %d", code)
	}
	if !waitGone(t, pid) {
		t.Fatal("服务应被结束")
	}
	if _, err := os.Stat(filepath.Join(dir, "task-a", "default.json")); !os.IsNotExist(err) {
		t.Fatal("端点应被删除")
	}

	// 再启动一个，任务释放时随任务结束。
	localCall(t, local, http.MethodPost, "/browsers", startBody)
	pid2, _ := waitEndpoint(t, dir, "task-a", "default")
	t.Cleanup(func() { killProcess(pid2) })
	var rel sandbox.ReleaseResult
	h.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &rel)
	if !waitGone(t, pid2) {
		t.Fatal("任务释放应结束浏览器服务")
	}
}

// 本机接口校验请求：任务与指派 ID 路径安全，身份名合法（它是端点文件名与浏览器配置目录名）。
func TestBrowsers_RejectsInvalidRequest(t *testing.T) {
	_, _, local := browserHarness(t)
	for _, body := range []string{
		`{"assignment_id":"asg","task_id":"task-a","identity":"../task-b/default"}`,
		`{"assignment_id":"asg","task_id":"task-a","identity":""}`,
		`{"assignment_id":"asg","task_id":"task-a","identity":"a.b"}`,
		`{"assignment_id":"asg","task_id":"../x","identity":"default"}`,
		`{"assignment_id":"../x","task_id":"task-a","identity":"default"}`,
	} {
		if code := localCall(t, local, http.MethodPost, "/browsers", body); code != http.StatusBadRequest {
			t.Errorf("%s: 应 400, got %d", body, code)
		}
	}
	if code := localCall(t, local, http.MethodDelete, "/browsers/a.b?task_id=task-a", ""); code != http.StatusBadRequest {
		t.Errorf("结束服务也应校验身份名, got %d", code)
	}
}

// 身份按任务隔离：不同任务的同名身份各有各的服务；释放一个任务不波及另一个，任务 ID 互为
// 前缀（task-a 与 task-a-1）也一样。
func TestBrowsers_SameIdentityIsolatedPerTask(t *testing.T) {
	h, dir, local := browserHarness(t)
	localCall(t, local, http.MethodPost, "/browsers", startBody)
	other := `{"assignment_id":"asg","task_id":"task-a-1","identity":"default"}`
	localCall(t, local, http.MethodPost, "/browsers", other)
	pid, _ := waitEndpoint(t, dir, "task-a", "default")
	otherPID, task := waitEndpoint(t, dir, "task-a-1", "default")
	if otherPID == pid || task != "task-a-1" {
		t.Fatalf("同名身份应各起一个归属各自任务的服务: %d/%d %s", pid, otherPID, task)
	}

	var rel sandbox.ReleaseResult
	h.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &rel)
	if !waitGone(t, pid) {
		t.Fatal("任务释放应结束本任务的服务")
	}
	if !processAlive(otherPID) {
		t.Fatal("释放 task-a 不应波及 task-a-1 的服务")
	}
	if again, _ := waitEndpoint(t, dir, "task-a-1", "default"); again != otherPID {
		t.Fatal("task-a-1 的端点应保持不变")
	}
	h.decode(t, http.MethodPost, "/tasks/task-a-1/release", nil, &rel)
	if !waitGone(t, otherPID) {
		t.Fatal("task-a-1 释放应结束它的服务")
	}
}

// 任务释放后不留端点：服务在删除自己的端点之前就退出（被结束、崩溃），以及上一沙盒实例
// 留下的端点，都由释放清掉，任务的端点目录随之删除。
func TestBrowsers_ReleaseLeavesNoEndpoint(t *testing.T) {
	h, dir, local := browserHarnessWith(t, abruptServiceScript)
	for _, identity := range []string{"default", "checker"} {
		localCall(t, local, http.MethodPost, "/browsers",
			`{"assignment_id":"asg","task_id":"task-a","identity":"`+identity+`"}`)
	}
	for _, identity := range []string{"default", "checker"} {
		waitEndpoint(t, dir, "task-a", identity)
	}
	if err := os.WriteFile(filepath.Join(dir, "task-a", "previous.json"), []byte(`{"port":1,"key":"k","pid":0}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var rel sandbox.ReleaseResult
	h.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &rel)
	if entries, err := os.ReadDir(filepath.Join(dir, "task-a")); !os.IsNotExist(err) {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("释放后任务的端点目录应已删除，残留 %v（%v）", names, err)
	}
}

// 任务释放后同一身份可立即重新启动（登记随释放撤销，不等后台回收）。
func TestBrowsers_RestartRightAfterRelease(t *testing.T) {
	h, dir, local := browserHarness(t)
	localCall(t, local, http.MethodPost, "/browsers", startBody)
	pid, _ := waitEndpoint(t, dir, "task-a", "default")
	t.Cleanup(func() { killProcess(pid) })
	var rel sandbox.ReleaseResult
	h.decode(t, http.MethodPost, "/tasks/task-a/release", nil, &rel)
	if _, err := os.Stat(filepath.Join(dir, "task-a", "default.json")); !os.IsNotExist(err) {
		t.Fatalf("释放应删除端点: %v", err)
	}

	localCall(t, local, http.MethodPost, "/browsers", startBody)
	pid2, _ := waitEndpoint(t, dir, "task-a", "default")
	t.Cleanup(func() { killProcess(pid2) })
	if pid2 == pid {
		t.Fatal("释放后应启动新的服务")
	}
}

// 同一身份的「强制结束」与「启动」并发：无论交错如何，结束后若有服务在运行，它的端点必须在
// （否则客户端永远连不上，又因服务在运行而不会再启动一个）。
func TestBrowsers_ConcurrentKillAndStartKeepEndpointConsistent(t *testing.T) {
	_, dir, local := browserHarness(t)
	for round := 0; round < 5; round++ {
		localCall(t, local, http.MethodPost, "/browsers", startBody)
		pid, _ := waitEndpoint(t, dir, "task-a", "default")
		t.Cleanup(func() { killProcess(pid) })

		done := make(chan struct{}, 2)
		go func() {
			_, _ = localDo(local, http.MethodDelete, "/browsers/default?task_id=task-a", "")
			done <- struct{}{}
		}()
		go func() {
			time.Sleep(time.Duration(round) * 5 * time.Millisecond)
			_, _ = localDo(local, http.MethodPost, "/browsers", startBody)
			done <- struct{}{}
		}()
		<-done
		<-done

		// 收敛后：要么没有服务也没有端点，要么端点指向一个活着的服务。
		time.Sleep(300 * time.Millisecond)
		if raw, err := os.ReadFile(filepath.Join(dir, "task-a", "default.json")); err == nil {
			var ep struct {
				PID int `json:"pid"`
			}
			_ = json.Unmarshal(raw, &ep)
			if !processAlive(ep.PID) {
				t.Fatalf("第 %d 轮：端点指向已结束的服务 %d", round, ep.PID)
			}
			t.Cleanup(func() { killProcess(ep.PID) })
			continue
		}
		// 没有端点：再请求一次启动，必须能起来（不会因为「已在运行」而什么也不做）。
		localCall(t, local, http.MethodPost, "/browsers", startBody)
		pid2, _ := waitEndpoint(t, dir, "task-a", "default")
		t.Cleanup(func() { killProcess(pid2) })
	}
}

// 固定复审给出的交错：强制结束的旧服务已退出、端点尚未清理时，另一条命令请求启动。新服务
// 起来并写了端点之后，旧的结束流程不得删掉它。
func TestBrowsers_KillDoesNotRemoveEndpointOfReplacement(t *testing.T) {
	_, dir, local := browserHarness(t)
	localCall(t, local, http.MethodPost, "/browsers", startBody)
	old, _ := waitEndpoint(t, dir, "task-a", "default")
	t.Cleanup(func() { killProcess(old) })

	var replacement int
	testHookBrowserKilled = func() {
		go func() { _, _ = localDo(local, http.MethodPost, "/browsers", startBody) }()
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if pid, _ := readEndpointPID(dir, "task-a", "default"); pid != 0 && pid != old {
				replacement = pid
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	t.Cleanup(func() { testHookBrowserKilled = nil })
	localCall(t, local, http.MethodDelete, "/browsers/default?task_id=task-a", "")
	testHookBrowserKilled = nil

	if replacement != 0 {
		t.Cleanup(func() { killProcess(replacement) })
	}
	pid, _ := readEndpointPID(dir, "task-a", "default")
	if pid == 0 || !processAlive(pid) {
		// 结束请求处理完后，并发的启动请求必须留下一个可连的服务。
		deadline := time.Now().Add(10 * time.Second)
		for (pid == 0 || !processAlive(pid)) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
			pid, _ = readEndpointPID(dir, "task-a", "default")
		}
		if pid == 0 || !processAlive(pid) {
			t.Fatalf("并发启动的服务应可连：端点 pid=%d", pid)
		}
		t.Cleanup(func() { killProcess(pid) })
	}
}

func readEndpointPID(dir, taskID, identity string) (int, error) {
	raw, err := os.ReadFile(filepath.Join(dir, taskID, identity+".json"))
	if err != nil {
		return 0, err
	}
	var ep struct {
		PID int `json:"pid"`
	}
	err = json.Unmarshal(raw, &ep)
	return ep.PID, err
}
