package runners

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBuildDockerArgs_Basic(t *testing.T) {
	spec := RunSpec{
		Image:      "alpine:3.19",
		Cmd:        []string{"echo", "hello"},
		AutoRemove: true,
	}
	got := buildDockerArgs(spec)
	want := []string{"run", "-i", "--add-host=host.docker.internal:host-gateway", "--rm", "alpine:3.19", "echo", "hello"}
	if !strSliceEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

func TestBuildDockerArgs_Full(t *testing.T) {
	spec := RunSpec{
		Image:      "liusha/pentools:v1",
		Cmd:        []string{"sqlmap", "-r", "/work/req.raw"},
		Workdir:    "/tmp/x",
		Network:    "liusha_scan_net",
		Env:        map[string]string{"FOO": "bar"},
		MemLimit:   512 * 1024 * 1024,
		CPULimit:   1.0,
		AutoRemove: true,
	}
	got := buildDockerArgs(spec)
	// 不强行断言完整顺序（env map 顺序非确定），只挑关键 flag。
	joined := strings.Join(got, " ")
	for _, want := range []string{
		"run -i --add-host=host.docker.internal:host-gateway --rm",
		"-v /tmp/x:/work",
		"--network liusha_scan_net",
		"-e FOO=bar",
		"--memory 536870912",
		"--cpus 1.00",
		"liusha/pentools:v1 sqlmap -r /work/req.raw",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("flag 缺失 %q: %s", want, joined)
		}
	}
}

func TestRunAndWait_RequiresImage(t *testing.T) {
	r := NewDockerRunner()
	if _, err := r.RunAndWait(context.Background(), RunSpec{}); err == nil {
		t.Fatal("空 Image 应报错")
	}
}

func TestNewDockerRunner_DefaultConcurrency(t *testing.T) {
	r := NewDockerRunner()
	if r.sem == nil {
		t.Fatal("sem 应被初始化")
	}
	if cap(r.sem) != fallbackConcurrency {
		t.Fatalf("默认并发应为 %d，实际 %d", fallbackConcurrency, cap(r.sem))
	}
}

func TestNewDockerRunner_WithConcurrency(t *testing.T) {
	r := NewDockerRunner(WithConcurrency(3))
	if cap(r.sem) != 3 {
		t.Fatalf("并发应为 3，实际 %d", cap(r.sem))
	}
	// n<=0 退化默认
	r2 := NewDockerRunner(WithConcurrency(0))
	if cap(r2.sem) != fallbackConcurrency {
		t.Fatalf("n<=0 应退化为默认，实际 %d", cap(r2.sem))
	}
}

func TestBuildDockerArgs_ContainerName(t *testing.T) {
	spec := RunSpec{
		Image:         "alpine:3.19",
		Cmd:           []string{"true"},
		ContainerName: "liusha-sqlmap-abc12345",
		AutoRemove:    true,
	}
	got := buildDockerArgs(spec)
	want := []string{"run", "-i", "--add-host=host.docker.internal:host-gateway", "--rm", "--name", "liusha-sqlmap-abc12345", "alpine:3.19", "true"}
	if !strSliceEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
}

// TestRunAndWait_HelloWorld 真实跑一个 alpine echo，验证端到端。
// 当 docker 不可用时 skip（CI 上无 docker 也不阻断）。
func TestRunAndWait_HelloWorld(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker 未安装，跳过")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon 未启动，跳过")
	}

	r := NewDockerRunner()
	res, err := r.RunAndWait(context.Background(), RunSpec{
		Image:      "alpine:3.19",
		Cmd:        []string{"echo", "liusha-runner-ok"},
		AutoRemove: true,
		Timeout:    60 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunAndWait err: %v stderr=%s", err, res.Stderr)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit=%d stderr=%s", res.ExitCode, res.Stderr)
	}
	if !strings.Contains(res.Stdout, "liusha-runner-ok") {
		t.Fatalf("stdout 应含 echo 输出: %q", res.Stdout)
	}
	if res.TimedOut {
		t.Fatalf("不应 timeout")
	}
}

// TestRunAndWait_NonZeroExit 验证容器非 0 退出不返回 err，由 ExitCode 反映。
func TestRunAndWait_NonZeroExit(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker 未安装")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon 未启动")
	}
	r := NewDockerRunner()
	res, err := r.RunAndWait(context.Background(), RunSpec{
		Image:      "alpine:3.19",
		Cmd:        []string{"sh", "-c", "exit 42"},
		AutoRemove: true,
		Timeout:    30 * time.Second,
	})
	if err != nil {
		t.Fatalf("非 0 退出不应返回 err，got: %v", err)
	}
	if res.ExitCode != 42 {
		t.Fatalf("expected ExitCode=42 got %d", res.ExitCode)
	}
}

// TestRunAndWait_TimeoutKillsContainer 验证 Timeout 触发后容器被显式 docker kill 杀死，
// 不会留下孤儿容器在 daemon 里继续跑（context cancel 只杀 docker CLI 进程，容器需额外 kill）。
func TestRunAndWait_TimeoutKillsContainer(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker 未安装")
	}
	if err := exec.Command("docker", "info").Run(); err != nil {
		t.Skip("docker daemon 未启动")
	}

	containerName := "liusha-test-timeout-" + strconv.FormatInt(time.Now().UnixNano(), 16)
	r := NewDockerRunner()
	res, err := r.RunAndWait(context.Background(), RunSpec{
		Image:         "alpine:3.19",
		Cmd:           []string{"sleep", "60"},
		ContainerName: containerName,
		AutoRemove:    true,
		Timeout:       2 * time.Second,
	})
	if err != nil {
		t.Fatalf("RunAndWait err: %v", err)
	}
	if !res.TimedOut {
		t.Fatalf("应触发 timeout，TimedOut=false")
	}

	// 容器名应 5s 内消失（kill 已发 + --rm 自动清理）。
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("docker", "ps", "-a", "-q", "--filter", "name="+containerName).Output()
		if len(strings.TrimSpace(string(out))) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	_ = exec.Command("docker", "rm", "-f", containerName).Run()
	t.Fatalf("容器 %s 在 timeout 后 5s 内仍存在 — orphan 检测失败", containerName)
}

func strSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
