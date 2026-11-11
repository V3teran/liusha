package sandbox

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// recordingClient 记下每条 exec 的命令，模拟容器内 sandbox-server。
type recordingClient struct {
	mu       sync.Mutex
	commands []string
}

func (c *recordingClient) Exec(_ context.Context, req ExecRequest) (ExecResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.commands = append(c.commands, req.Command)
	return ExecResult{}, nil
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

type fakeLauncher struct{ client *recordingClient }

func (l *fakeLauncher) Spawn(context.Context, string) (Client, error) { return l.client, nil }
func (l *fakeLauncher) Destroy(context.Context, string) error         { return nil }
func (l *fakeLauncher) CleanupOrphans(context.Context) error          { return nil }

func newTestWarmPool(t *testing.T) (*WarmPoolManager, *recordingClient) {
	t.Helper()
	client := &recordingClient{}
	m := NewWarmPoolManager(&fakeLauncher{client: client}, zerolog.Nop(), WarmPoolConfig{
		IdleTimeout:       time.Hour,
		HealthCheckPeriod: time.Hour,
	})
	t.Cleanup(func() { _ = m.Shutdown(context.Background()) })
	return m, client
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
