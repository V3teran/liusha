package sandbox

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// recordingClient 记下每条 exec 的命令，模拟容器内 sandbox-server。
type recordingClient struct {
	mu          sync.Mutex
	commands    []string
	probeFailed bool // 健康探测（echo healthy）返回错误
}

func (c *recordingClient) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	if err := ctx.Err(); err != nil {
		return ExecResult{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commands = append(c.commands, req.Command)
	if c.probeFailed && req.Command == "echo healthy" {
		return ExecResult{}, errors.New("probe timeout")
	}
	return ExecResult{}, nil
}

func (c *recordingClient) failProbes() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.probeFailed = true
}

func (c *recordingClient) Close() error { return nil }

// cleanupRuns 统计清理脚本（杀进程）被执行的次数。
func (c *recordingClient) cleanupRuns() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := 0
	for _, cmd := range c.commands {
		if strings.Contains(cmd, "kill -TERM") {
			n++
		}
	}
	return n
}

type fakeLauncher struct {
	client    *recordingClient
	destroyed atomic.Int32
}

func (l *fakeLauncher) Spawn(ctx context.Context, _ string) (Client, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return l.client, nil
}
func (l *fakeLauncher) Destroy(context.Context, string) error { l.destroyed.Add(1); return nil }
func (l *fakeLauncher) CleanupOrphans(context.Context) error  { return nil }

func newTestWarmPool(t *testing.T) (*WarmPoolManager, *recordingClient) {
	m, client, _ := newTestWarmPoolWithLauncher(t)
	return m, client
}

func newTestWarmPoolWithLauncher(t *testing.T) (*WarmPoolManager, *recordingClient, *fakeLauncher) {
	t.Helper()
	client := &recordingClient{}
	l := &fakeLauncher{client: client}
	m := NewWarmPoolManager(l, zerolog.Nop(), WarmPoolConfig{
		IdleTimeout:       time.Hour,
		HealthCheckPeriod: time.Hour,
	})
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m, client, l
}

// 暖池容器由并发任务共享：一个任务释放时，容器内其他任务的进程不得被清理脚本误杀。
func TestWarmPool_ReleaseDoesNotKillConcurrentTasks(t *testing.T) {
	ctx := context.Background()
	m, client := newTestWarmPool(t)

	a, err := m.Acquire(ctx, AcquireRequest{AssignmentID: "asg", TaskID: "task-a"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Acquire(ctx, AcquireRequest{AssignmentID: "asg", TaskID: "task-b"}); err != nil {
		t.Fatal(err)
	}
	before := client.cleanupRuns()

	if err := m.Release(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got := client.cleanupRuns(); got != before {
		t.Fatalf("task-b 仍持有容器时释放 task-a 不应执行清理脚本（执行了 %d 次）", got-before)
	}
}

// 容器空闲后再借出：借出前清理上一轮残留（浏览器 / 进程 / 临时文件）。
func TestWarmPool_IdleBorrowCleansLeftovers(t *testing.T) {
	ctx := context.Background()
	m, client := newTestWarmPool(t)

	a, err := m.Acquire(ctx, AcquireRequest{AssignmentID: "asg", TaskID: "task-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Release(ctx, a); err != nil {
		t.Fatal(err)
	}
	before := client.cleanupRuns()

	if _, err := m.Acquire(ctx, AcquireRequest{AssignmentID: "asg", TaskID: "task-b"}); err != nil {
		t.Fatal(err)
	}
	if got := client.cleanupRuns(); got != before+1 {
		t.Fatalf("空闲容器借出前应清理一次，实际 %d 次", got-before)
	}
}

// 共享容器仍有持有者时，新借用者的探测失败不得销毁容器：探测超时在负载下可能是误判，
// 而持有者正在用它；真坏了由维护循环在空闲时回收。
func TestWarmPool_ProbeFailureWithHoldersKeepsContainer(t *testing.T) {
	ctx := context.Background()
	m, client, l := newTestWarmPoolWithLauncher(t)
	if _, err := m.Acquire(ctx, AcquireRequest{AssignmentID: "asg", TaskID: "task-a"}); err != nil {
		t.Fatal(err)
	}
	client.failProbes()

	if _, err := m.Acquire(ctx, AcquireRequest{AssignmentID: "asg", TaskID: "task-b"}); err != nil {
		t.Fatalf("有持有者时探测失败应照常借出共享容器，得 %v", err)
	}
	if n := l.destroyed.Load(); n != 0 {
		t.Fatalf("task-a 仍在使用时不应销毁容器（销毁 %d 次）", n)
	}
}

// 借用请求自身已取消：返回取消错误，不把它当成容器故障。
func TestWarmPool_CanceledAcquireDoesNotDestroy(t *testing.T) {
	m, _, l := newTestWarmPoolWithLauncher(t)
	if _, err := m.Acquire(context.Background(), AcquireRequest{AssignmentID: "asg", TaskID: "task-a"}); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := m.Acquire(canceled, AcquireRequest{AssignmentID: "asg", TaskID: "task-b"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("应返回取消错误，得 %v", err)
	}
	if n := l.destroyed.Load(); n != 0 {
		t.Fatalf("请求取消不应销毁容器（销毁 %d 次）", n)
	}
}

// 无持有者时探测失败：销毁并重建（原有自愈行为）。
func TestWarmPool_ProbeFailureWhenIdleRecreates(t *testing.T) {
	ctx := context.Background()
	m, client, l := newTestWarmPoolWithLauncher(t)
	a, err := m.Acquire(ctx, AcquireRequest{AssignmentID: "asg", TaskID: "task-a"})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Release(ctx, a); err != nil {
		t.Fatal(err)
	}
	client.failProbes()

	if _, err := m.Acquire(ctx, AcquireRequest{AssignmentID: "asg", TaskID: "task-b"}); err != nil {
		t.Fatal(err)
	}
	if n := l.destroyed.Load(); n != 1 {
		t.Fatalf("空闲且探测失败应销毁重建一次，得 %d", n)
	}
}
