package core

import (
	"context"
	"sync"
	"testing"
)

// TestOptimisticLock_Concurrent 测试并发更新场景下的乐观锁
func TestOptimisticLock_Concurrent(t *testing.T) {
	store := NewInMemoryGraphStore()
	ctx := context.Background()

	// 创建测试节点
	node := &GraphNode{
		ID:      "test-node-1",
		Kind:    "action",
		State:   "open",
		Content: []byte(`{"test": "data"}`),
		Version: 1,
	}

	err := store.CreateNode(ctx, node)
	if err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}

	// 先读取节点，获取初始版本
	initialNode, err := store.GetNode(ctx, "test-node-1")
	if err != nil {
		t.Fatalf("读取初始节点失败: %v", err)
	}
	initialVersion := initialNode.Version

	// 并发更新：10 个 goroutine 同时尝试用**相同的初始版本**更新
	const numGoroutines = 10
	var wg sync.WaitGroup
	successCount := 0
	failCount := 0
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// 所有 goroutine 使用相同的初始版本尝试更新
			err := store.UpdateNode(ctx, "test-node-1", GraphNodeUpdate{
				State:           "updated",
				ExpectedVersion: &initialVersion,
			})

			mu.Lock()
			if err == nil {
				successCount++
			} else if err == ErrVersionMismatch {
				failCount++
			} else {
				t.Errorf("goroutine %d: 更新失败（非版本冲突）: %v", id, err)
			}
			mu.Unlock()
		}(i)
	}

	wg.Wait()

	// 验证结果：只有 1 个成功，其余 9 个失败（版本冲突）
	if successCount != 1 {
		t.Errorf("期望只有 1 个 goroutine 成功更新，实际: %d", successCount)
	}

	if failCount != numGoroutines-1 {
		t.Errorf("期望 %d 个 goroutine 因版本冲突失败，实际: %d", numGoroutines-1, failCount)
	}

	// 验证最终状态
	finalNode, err := store.GetNode(ctx, "test-node-1")
	if err != nil {
		t.Fatalf("读取最终节点失败: %v", err)
	}

	if finalNode.State != "updated" {
		t.Errorf("期望状态 'updated'，实际: %s", finalNode.State)
	}

	if finalNode.Version != 2 {
		t.Errorf("期望版本 2，实际: %d", finalNode.Version)
	}
}

// TestOptimisticLock_Sequential 测试顺序更新（无冲突）
func TestOptimisticLock_Sequential(t *testing.T) {
	store := NewInMemoryGraphStore()
	ctx := context.Background()

	// 创建测试节点
	node := &GraphNode{
		ID:      "test-node-2",
		Kind:    "action",
		State:   "open",
		Content: []byte(`{"test": "data"}`),
		Version: 1,
	}

	err := store.CreateNode(ctx, node)
	if err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}

	// 顺序更新 5 次
	for i := 1; i <= 5; i++ {
		// 读取当前节点
		currentNode, err := store.GetNode(ctx, "test-node-2")
		if err != nil {
			t.Fatalf("第 %d 次读取失败: %v", i, err)
		}

		// 使用乐观锁更新
		version := currentNode.Version
		err = store.UpdateNode(ctx, "test-node-2", GraphNodeUpdate{
			State:           "running",
			ExpectedVersion: &version,
		})

		if err != nil {
			t.Fatalf("第 %d 次更新失败: %v", i, err)
		}

		// 验证 version 递增
		updatedNode, err := store.GetNode(ctx, "test-node-2")
		if err != nil {
			t.Fatalf("第 %d 次读取更新后节点失败: %v", i, err)
		}

		expectedVersion := int64(i + 1)
		if updatedNode.Version != expectedVersion {
			t.Errorf("第 %d 次更新后期望版本 %d，实际: %d", i, expectedVersion, updatedNode.Version)
		}
	}
}

// TestOptimisticLock_VersionMismatch 测试版本不匹配时的错误
func TestOptimisticLock_VersionMismatch(t *testing.T) {
	store := NewInMemoryGraphStore()
	ctx := context.Background()

	// 创建测试节点
	node := &GraphNode{
		ID:      "test-node-3",
		Kind:    "action",
		State:   "open",
		Content: []byte(`{"test": "data"}`),
		Version: 1,
	}

	err := store.CreateNode(ctx, node)
	if err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}

	// 先更新一次（version 变为 2）
	version1 := int64(1)
	err = store.UpdateNode(ctx, "test-node-3", GraphNodeUpdate{
		State:           "running",
		ExpectedVersion: &version1,
	})
	if err != nil {
		t.Fatalf("第一次更新失败: %v", err)
	}

	// 尝试用旧的 version (1) 再次更新
	err = store.UpdateNode(ctx, "test-node-3", GraphNodeUpdate{
		State:           "completed",
		ExpectedVersion: &version1,
	})

	if err != ErrVersionMismatch {
		t.Errorf("期望 ErrVersionMismatch 错误，实际: %v", err)
	}

	// 验证状态没有被错误更新
	finalNode, err := store.GetNode(ctx, "test-node-3")
	if err != nil {
		t.Fatalf("读取最终节点失败: %v", err)
	}

	if finalNode.State != "running" {
		t.Errorf("期望状态 'running'（未被更新），实际: %s", finalNode.State)
	}

	if finalNode.Version != 2 {
		t.Errorf("期望版本 2，实际: %d", finalNode.Version)
	}
}

// TestCompareAndSwapState_Concurrent 测试 CAS 并发场景
func TestCompareAndSwapState_Concurrent(t *testing.T) {
	store := NewInMemoryGraphStore()
	ctx := context.Background()

	// 创建测试节点
	node := &GraphNode{
		ID:      "test-node-4",
		Kind:    "action",
		State:   "open",
		Content: []byte(`{"test": "data"}`),
		Version: 1,
	}

	err := store.CreateNode(ctx, node)
	if err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}

	// 并发 CAS：10 个 goroutine 同时尝试从 open → running
	const numGoroutines = 10
	var wg sync.WaitGroup
	successCount := 0
	var mu sync.Mutex

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			success, err := store.CompareAndSwapState(ctx, "test-task", "test-node-4", "open", "running")
			if err != nil {
				t.Errorf("goroutine %d: CAS 失败: %v", id, err)
				return
			}

			if success {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()

	// 验证结果：只有 1 个成功
	if successCount != 1 {
		t.Errorf("期望只有 1 个 goroutine 成功 CAS，实际: %d", successCount)
	}

	// 验证最终状态
	finalNode, err := store.GetNode(ctx, "test-node-4")
	if err != nil {
		t.Fatalf("读取最终节点失败: %v", err)
	}

	if finalNode.State != "running" {
		t.Errorf("期望状态 'running'，实际: %s", finalNode.State)
	}

	if finalNode.Version != 2 {
		t.Errorf("期望版本 2（CAS 成功后递增），实际: %d", finalNode.Version)
	}
}

// TestCompareAndSwapState_StateMismatch 测试 CAS 状态不匹配
func TestCompareAndSwapState_StateMismatch(t *testing.T) {
	store := NewInMemoryGraphStore()
	ctx := context.Background()

	// 创建测试节点（状态为 running）
	node := &GraphNode{
		ID:      "test-node-5",
		Kind:    "action",
		State:   "running",
		Content: []byte(`{"test": "data"}`),
		Version: 1,
	}

	err := store.CreateNode(ctx, node)
	if err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}

	// 尝试 CAS：期望 open → completed（但实际是 running）
	success, err := store.CompareAndSwapState(ctx, "test-task", "test-node-5", "open", "completed")
	if err != nil {
		t.Fatalf("CAS 失败: %v", err)
	}

	if success {
		t.Error("期望 CAS 失败（状态不匹配），但返回 success=true")
	}

	// 验证状态没有被修改
	finalNode, err := store.GetNode(ctx, "test-node-5")
	if err != nil {
		t.Fatalf("读取最终节点失败: %v", err)
	}

	if finalNode.State != "running" {
		t.Errorf("期望状态 'running'（未被修改），实际: %s", finalNode.State)
	}

	if finalNode.Version != 1 {
		t.Errorf("期望版本 1（未被修改），实际: %d", finalNode.Version)
	}
}

// TestOptimisticLock_UpdateWithoutVersion 测试不使用乐观锁的更新
func TestOptimisticLock_UpdateWithoutVersion(t *testing.T) {
	store := NewInMemoryGraphStore()
	ctx := context.Background()

	// 创建测试节点
	node := &GraphNode{
		ID:      "test-node-6",
		Kind:    "action",
		State:   "open",
		Content: []byte(`{"test": "data"}`),
		Version: 1,
	}

	err := store.CreateNode(ctx, node)
	if err != nil {
		t.Fatalf("创建节点失败: %v", err)
	}

	// 不使用乐观锁更新（ExpectedVersion = nil）
	err = store.UpdateNode(ctx, "test-node-6", GraphNodeUpdate{
		State: "running",
		// ExpectedVersion 为 nil，跳过版本检查
	})

	if err != nil {
		t.Fatalf("更新失败: %v", err)
	}

	// 验证更新成功
	updatedNode, err := store.GetNode(ctx, "test-node-6")
	if err != nil {
		t.Fatalf("读取更新后节点失败: %v", err)
	}

	if updatedNode.State != "running" {
		t.Errorf("期望状态 'running'，实际: %s", updatedNode.State)
	}

	if updatedNode.Version != 2 {
		t.Errorf("期望版本 2（更新后递增），实际: %d", updatedNode.Version)
	}
}
