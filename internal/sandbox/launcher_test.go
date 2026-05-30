package sandbox

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// testImage 是集成测试用的 sandbox 镜像；和 design doc + Dockerfile 一致。
const testImage = "liusha/pentools:latest"

// skipIfNoDocker 检查 docker 二进制 + daemon 可达；不满足 t.Skip。
func skipIfNoDocker(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker not in PATH, skipping integration test")
	}
	if err := exec.Command("docker", "version", "--format", "{{.Server.Version}}").Run(); err != nil {
		t.Skip("docker daemon not reachable, skipping integration test")
	}
}

// skipIfNoImage 检查指定镜像本地是否存在；不存在 t.Skip。
// 不主动 docker pull——CI 环境镜像应提前 build，避免测试时长不可控。
func skipIfNoImage(t *testing.T, image string) {
	t.Helper()
	out, err := exec.Command("docker", "images", "-q", image).Output()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		t.Skipf("image %s not present locally, skipping (build it first)", image)
	}
}

// testRunID 生成集成测试用的 runID（带 ns 时间戳唯一）。
func testRunID(t *testing.T, prefix string) string {
	t.Helper()
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}

// TestDockerLauncher_SpawnExecDestroy：端到端跑通 Spawn → Exec → Destroy。
func TestDockerLauncher_SpawnExecDestroy(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoImage(t, testImage)

	l := NewDockerLauncher(testImage)
	runID := testRunID(t, "test-spawnexec")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client, err := l.Spawn(ctx, runID)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() {
		if err := l.Destroy(context.Background(), runID); err != nil {
			t.Logf("Destroy 失败（允许 best-effort）: %v", err)
		}
	})

	res, err := client.Exec(ctx, ExecRequest{
		HunterID:       runID,
		Command:        "echo from-test && echo err-test >&2",
		TimeoutSeconds: 5,
		Tag:            "integration",
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exit_code=%d, want 0", res.ExitCode)
	}
	if !strings.Contains(res.Stdout, "from-test") {
		t.Errorf("stdout=%q, want contains 'from-test'", res.Stdout)
	}
	if !strings.Contains(res.Stderr, "err-test") {
		t.Errorf("stderr=%q, want contains 'err-test'", res.Stderr)
	}
	if res.TimedOut {
		t.Errorf("timed_out should be false")
	}
}

// TestDockerLauncher_OutputDirAttachment：$OUTPUT_DIR 写文件 → 附件返回。
func TestDockerLauncher_OutputDirAttachment(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoImage(t, testImage)

	l := NewDockerLauncher(testImage)
	runID := testRunID(t, "test-attach")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client, err := l.Spawn(ctx, runID)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() {
		_ = l.Destroy(context.Background(), runID)
	})

	res, err := client.Exec(ctx, ExecRequest{
		HunterID:       runID,
		Command:        `printf 'file-content\n' > "$OUTPUT_DIR/test.txt"`,
		TimeoutSeconds: 5,
		Tag:            "attach",
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if len(res.Files) != 1 {
		t.Fatalf("files=%+v, want 1 file", res.Files)
	}
	if res.Files[0].Name != "test.txt" {
		t.Errorf("name=%q, want 'test.txt'", res.Files[0].Name)
	}
	// base64("file-content\n") = "ZmlsZS1jb250ZW50Cg=="
	if res.Files[0].B64 != "ZmlsZS1jb250ZW50Cg==" {
		t.Errorf("b64=%q, want 'ZmlsZS1jb250ZW50Cg=='", res.Files[0].B64)
	}
}

// TestDockerLauncher_Timeout：超时命令返回 timed_out=true。
func TestDockerLauncher_Timeout(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoImage(t, testImage)

	l := NewDockerLauncher(testImage)
	runID := testRunID(t, "test-timeout")

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client, err := l.Spawn(ctx, runID)
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() {
		_ = l.Destroy(context.Background(), runID)
	})

	res, err := client.Exec(ctx, ExecRequest{
		HunterID:       runID,
		Command:        "sleep 10",
		TimeoutSeconds: 1,
		Tag:            "to",
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if !res.TimedOut {
		t.Errorf("expected timed_out=true, got %+v", res)
	}
}

// TestDockerLauncher_CleanupOrphans：未主动 Destroy 的容器被 CleanupOrphans 清理。
func TestDockerLauncher_CleanupOrphans(t *testing.T) {
	skipIfNoDocker(t)
	skipIfNoImage(t, testImage)

	l := NewDockerLauncher(testImage)
	runID := testRunID(t, "test-orphan")
	name := containerNamePrefix + runID

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	if _, err := l.Spawn(ctx, runID); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	t.Cleanup(func() {
		// 双重清理保险——CleanupOrphans 该清的，但万一没清也别留垃圾
		_ = l.Destroy(context.Background(), runID)
	})

	// 不主动 Destroy，直接 CleanupOrphans
	if err := l.CleanupOrphans(ctx); err != nil {
		t.Fatalf("CleanupOrphans: %v", err)
	}

	// 验证容器已经被清掉
	out, _ := exec.Command("docker", "ps", "-a", "--filter=name="+name, "--format={{.Names}}").Output()
	if remaining := strings.TrimSpace(string(out)); remaining != "" {
		t.Errorf("orphan still present: %s", remaining)
	}
}
