package browser

import (
	"bufio"
	"context"
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
	out, err := exec.Command(filepath.Join(bin, "browser-use"), "open", "https://a b").Output() // #nosec G204 // 测试内生成的脚本
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(dir, "client.py") + " open https://a b\n"
	if string(out) != want {
		t.Fatalf("包装脚本输出 %q, 期望 %q", out, want)
	}
}

// fakeService 在 sock 上模拟一个浏览器服务：收到一行请求回 {"code":0} 后关闭，记录收到的请求。
func fakeService(t *testing.T, sock string) <-chan string {
	t.Helper()
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	got := make(chan string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		line, _ := bufio.NewReader(conn).ReadString('\n')
		got <- strings.TrimSpace(line)
		_, _ = conn.Write([]byte(`{"stdout":"ok\n","stderr":"","code":0}`))
	}()
	return got
}

// 按任务关闭：只向本任务的身份发 reset；残留套接字（服务已退出）被清掉且不计数。
func TestReleaseTask_OnlyOwnTask(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "lsb") // unix 套接字路径有长度上限，t.TempDir 在部分系统上过长
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	old := socketDir
	socketDir = dir
	t.Cleanup(func() { socketDir = old })

	own := fakeService(t, filepath.Join(dir, "browser-svc-task1-default.sock"))
	other := fakeService(t, filepath.Join(dir, "browser-svc-task2-default.sock"))
	stale := filepath.Join(dir, "browser-svc-task1-admin.sock")
	if err := os.WriteFile(stale, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	n, err := ReleaseTask(context.Background(), "task1")
	if err != nil || n != 1 {
		t.Fatalf("应关闭 1 个会话: n=%d err=%v", n, err)
	}
	if req := <-own; req != `{"sub":"reset"}` {
		t.Fatalf("应发送 reset, got %q", req)
	}
	select {
	case req := <-other:
		t.Fatalf("其他任务的会话不应收到请求: %q", req)
	default:
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("残留套接字应被清掉: %v", err)
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
