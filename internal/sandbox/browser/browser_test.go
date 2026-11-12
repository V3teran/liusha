package browser

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// 释放脚本：client.py / service.py 落盘，browser-use 包装脚本以指定解释器运行客户端。
func TestInstall_WritesScriptsAndWrapper(t *testing.T) {
	dir := t.TempDir()
	// 用 echo 充当解释器：包装脚本执行后应把客户端路径与参数原样交给它。
	bin, err := Install(dir, "echo")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"client.py", "service.py"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("%s 未释放: %v", name, err)
		}
	}
	// 包装脚本由沙盒的 POSIX shell 执行（Windows 不能直接执行 #! 脚本）。
	sh, err := exec.LookPath("sh")
	if err != nil {
		t.Skip("本机没有 sh")
	}
	out, err := exec.Command(sh, filepath.Join(bin, "browser-use"), "open", "https://a b").Output() // #nosec G204 // 测试内生成的脚本
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.ToSlash(filepath.Join(dir, "client.py")) + " open https://a b\n"
	if string(out) != want {
		t.Fatalf("包装脚本输出 %q, 期望 %q", out, want)
	}
}

// fakeService 模拟一个浏览器服务：在回环地址监听、把端点写进 dir/<identity>.json；收到一行
// 请求后按口令回包——口令对回 reply，不对回 unauthorized——并把请求交给调用方核对。
func fakeService(t *testing.T, dir, identity, reply string) <-chan map[string]any {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	key := identity + "-key"
	writeEndpoint(t, dir, identity, ln.Addr().(*net.TCPAddr).Port, key)
	got := make(chan map[string]any, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		var req map[string]any
		_ = json.Unmarshal([]byte(line), &req)
		got <- req
		if req["key"] != key {
			_, _ = conn.Write([]byte(`{"unauthorized": true}`))
			return
		}
		_, _ = conn.Write([]byte(reply))
	}()
	return got
}

func writeEndpoint(t *testing.T, dir, identity string, port int, key string) {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"port": port, "key": key})
	if err := os.WriteFile(filepath.Join(dir, identity+".json"), b, 0o600); err != nil {
		t.Fatal(err)
	}
}

// freePort 返回一个当前无人监听的回环端口。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port
}

// 按任务关闭：只向本任务的身份发带口令的 reset；残留端点（服务已退出、或端口被别的服务占用
// 而口令不符）被清掉且不计数。
func TestReleaseTask_OnlyOwnTask(t *testing.T) {
	dir := t.TempDir()
	const ok = `{"stdout":"ok\n","stderr":"","code":0}`
	own := fakeService(t, dir, "task1-default", ok)
	other := fakeService(t, dir, "task2-default", ok)
	writeEndpoint(t, dir, "task1-admin", freePort(t), "k")
	reused := fakeService(t, dir, "task1-reused", ok)
	writeEndpoint(t, dir, "task1-reused", portOf(t, dir, "task1-reused"), "旧服务的口令")

	n, err := ReleaseTask(context.Background(), dir, "task1")
	if err != nil || n != 1 {
		t.Fatalf("应关闭 1 个会话: n=%d err=%v", n, err)
	}
	if req := <-own; req["sub"] != "reset" || req["key"] != "task1-default-key" {
		t.Fatalf("应发送带口令的 reset, got %v", req)
	}
	<-reused
	select {
	case req := <-other:
		t.Fatalf("其他任务的会话不应收到请求: %v", req)
	default:
	}
	for _, stale := range []string{"task1-admin", "task1-reused"} {
		if _, err := os.Stat(filepath.Join(dir, stale+".json")); !os.IsNotExist(err) {
			t.Errorf("残留端点 %s 应被清掉: %v", stale, err)
		}
	}
}

func portOf(t *testing.T, dir, identity string) int {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, identity+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var ep endpoint
	if err := json.Unmarshal(b, &ep); err != nil {
		t.Fatal(err)
	}
	return ep.Port
}

// testPython 返回本机的 Python 解释器；没有则跳过（协议测试需要真实运行 client.py）。
func testPython(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"python3", "python"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("本机没有 Python")
	return ""
}

// 客户端与服务的协议：读端点、带口令发请求，请求里带 tab 归属、run 归属、产物目录与回灌中继；
// 回放服务给出的输出与退出码。
func TestClient_SpeaksServiceProtocol(t *testing.T) {
	python := testPython(t)
	dir := t.TempDir()
	if _, err := Install(dir, python); err != nil {
		t.Fatal(err)
	}
	endpoints := t.TempDir()
	got := fakeService(t, endpoints, "task1-default", `{"stdout":"hello\n","stderr":"","code":3}`)

	cmd := exec.Command(python, filepath.Join(dir, "client.py"), "state") // #nosec G204 // 测试内释放的脚本
	cmd.Env = append(os.Environ(),
		"IDENTITY=task1-default", "AGENT_ID=act-1", "LIUSHA_AGENT_RUN_ID=run-1", "OUTPUT_DIR=/out",
		"LIUSHA_BROWSER_DIR="+endpoints,
		"LIUSHA_INGEST_RELAY_URL=http://127.0.0.1:1/ingest", "LIUSHA_INGEST_RELAY_KEY=rk")
	out, err := cmd.Output()
	var ee *exec.ExitError
	if !errors.As(err, &ee) || ee.ExitCode() != 3 || string(out) != "hello\n" {
		t.Fatalf("应回放服务的输出与退出码: out=%q err=%v", out, err)
	}
	req := <-got
	want := map[string]any{"sub": "state", "key": "task1-default-key", "agent_id": "act-1",
		"agent_run_id": "run-1", "output_dir": "/out"}
	for k, v := range want {
		if req[k] != v {
			t.Errorf("请求字段 %s = %v，期望 %v", k, req[k], v)
		}
	}
	if ingest, _ := req["ingest"].(map[string]any); ingest["url"] != "http://127.0.0.1:1/ingest" || ingest["key"] != "rk" {
		t.Errorf("请求应带上回灌中继, got %v", req["ingest"])
	}
}

// 能力探测导入的名称与浏览器服务实际导入的第三方名称一致：服务脚本改了导入而探测没跟上，
// 探测就会把跑不起来的环境报成可用。
func TestProbeImports_MatchService(t *testing.T) {
	src, err := scripts.ReadFile("service.py")
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, line := range strings.Split(string(src), "\n") {
		if strings.HasPrefix(line, "from browser_use") || line == "import httpx" {
			want = append(want, line)
		}
	}
	got := strings.Split(probeImports, "\n")
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("探测导入与 service.py 不一致：\n探测 %q\n服务 %q", got, want)
	}
}

func TestProbe_MissingInterpreter(t *testing.T) {
	st := Probe(context.Background(), "no-such-python")
	if st.Installed || !strings.Contains(st.Detail, "no-such-python") {
		t.Fatalf("got %+v", st)
	}
}
