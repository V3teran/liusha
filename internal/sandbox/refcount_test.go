package sandbox

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// TestRefCountManager_BasicFlow 测试基本的 Acquire/Release 流程
func TestRefCountManager_BasicFlow(t *testing.T) {
	launcher := &mockLauncher{containers: make(map[string]*mockClient)}
	poolMgr := NewPooledManager(launcher, zerolog.Nop(), 1000) // 1秒 grace period
	refMgr := NewRefCountManager(poolMgr, zerolog.Nop(), 1*time.Hour)
	defer refMgr.Stop()

	ctx := context.Background()
	assignmentID := "test-assignment-001"

	// 1. 首次 Acquire
	client1, err := refMgr.Acquire(ctx, assignmentID)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	if client1 == nil {
		t.Fatal("client1 is nil")
	}

	stats := poolMgr.Stats()
	totalPools := stats["total_pools"].(int)
	if totalPools != 1 {
		t.Errorf("expected 1 container, got %d", totalPools)
	}
	activePools := stats["active_pools"].(int)
	if activePools != 1 {
		t.Errorf("expected 1 active container, got %d", activePools)
	}

	// 2. 第二次 Acquire（复用）
	client2, err := refMgr.Acquire(ctx, assignmentID)
	if err != nil {
		t.Fatalf("second acquire failed: %v", err)
	}
	if client2 != client1 {
		t.Error("expected to reuse same client")
	}

	stats = poolMgr.Stats()
	totalPools = stats["total_pools"].(int)
	if totalPools != 1 {
		t.Errorf("expected 1 container after reuse, got %d", totalPools)
	}

	// 3. 第一次 Release（refCount 降为 1）
	if err := refMgr.Release(ctx, assignmentID); err != nil {
		t.Fatalf("first release failed: %v", err)
	}

	stats = poolMgr.Stats()
	activePools = stats["active_pools"].(int)
	if activePools != 1 {
		t.Errorf("expected 1 active after first release, got %d", activePools)
	}

	// 4. 第二次 Release（refCount 降为 0）
	if err := refMgr.Release(ctx, assignmentID); err != nil {
		t.Fatalf("second release failed: %v", err)
	}

	stats = poolMgr.Stats()
	idlePools := stats["idle_pools"].(int)
	if idlePools != 1 {
		t.Errorf("expected 1 idle container, got %d", idlePools)
	}

	// 5. 等待 grace period 后容器应被清理
	time.Sleep(1500 * time.Millisecond)

	stats = poolMgr.Stats()
	totalPools = stats["total_pools"].(int)
	if totalPools != 0 {
		t.Errorf("expected 0 containers after grace period, got %d", totalPools)
	}

	if !launcher.containers[assignmentID].destroyed {
		t.Error("container was not destroyed")
	}
}

// TestRefCountManager_StaleCleanup 测试超时清理
func TestRefCountManager_StaleCleanup(t *testing.T) {
	launcher := &mockLauncher{containers: make(map[string]*mockClient)}
	poolMgr := NewPooledManager(launcher, zerolog.Nop(), 10000) // 10秒 grace period
	refMgr := NewRefCountManager(poolMgr, zerolog.Nop(), 100*time.Millisecond) // 100ms TTL
	defer refMgr.Stop()

	ctx := context.Background()
	assignmentID := "test-stale-001"

	// Acquire 容器
	_, err := refMgr.Acquire(ctx, assignmentID)
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}

	// Release 容器（但在 grace period 内不会销毁）
	if err := refMgr.Release(ctx, assignmentID); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	// 等待超过 TTL（但小于 grace period）
	time.Sleep(200 * time.Millisecond)

	// 手动触发清理
	refMgr.cleanupStaleContainers()

	// 容器应被强制销毁
	stats := poolMgr.Stats()
	totalPools := stats["total_pools"].(int)
	if totalPools != 0 {
		t.Errorf("expected 0 containers after stale cleanup, got %d", totalPools)
	}

	if !launcher.containers[assignmentID].destroyed {
		t.Error("stale container was not destroyed")
	}
}

// TestSafeReleaser_PanicRecovery 测试 panic 恢复
func TestSafeReleaser_PanicRecovery(t *testing.T) {
	launcher := &mockLauncher{containers: make(map[string]*mockClient)}
	poolMgr := NewPooledManager(launcher, zerolog.Nop(), 1000)
	safeRel := NewSafeReleaser(poolMgr, zerolog.Nop())

	ctx := context.Background()
	assignmentID := "test-panic-001"

	// 使用 WithContainer，在闭包中 panic
	err := safeRel.WithContainer(ctx, assignmentID, func(client Client) error {
		// 验证容器已分配
		if client == nil {
			t.Error("client is nil in WithContainer")
		}

		// 触发 panic
		panic("test panic")
	})

	// 应该捕获 panic 并返回 error
	if err == nil {
		t.Fatal("expected error from panic, got nil")
	}
	if err.Error() != "panic in container operation: test panic" {
		t.Errorf("unexpected error: %v", err)
	}

	// 容器应该被正确释放
	stats := poolMgr.Stats()
	idlePools := stats["idle_pools"].(int)
	if idlePools != 1 {
		t.Errorf("expected container to be released after panic, got %d idle", idlePools)
	}
}

// mockLauncher 用于测试的 mock launcher
type mockLauncher struct {
	containers map[string]*mockClient
}

func (m *mockLauncher) Spawn(ctx context.Context, assignmentID string) (Client, error) {
	client := &mockClient{assignmentID: assignmentID}
	m.containers[assignmentID] = client
	return client, nil
}

func (m *mockLauncher) Destroy(ctx context.Context, assignmentID string) error {
	if client, ok := m.containers[assignmentID]; ok {
		client.destroyed = true
	}
	return nil
}

func (m *mockLauncher) CleanupOrphans(ctx context.Context) error {
	return nil
}

// mockClient 用于测试的 mock client
type mockClient struct {
	assignmentID string
	destroyed    bool
}

func (m *mockClient) Exec(ctx context.Context, req ExecRequest) (ExecResult, error) {
	return ExecResult{}, nil
}

func (m *mockClient) Close() error {
	return nil
}
