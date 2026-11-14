package explorationgraph_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/google/uuid"
)

// TestDynamicActionGeneration 验证动态生成 Actions
//
// 场景：模拟 ReAct 循环中 Planner 根据观察结果动态生成新 Actions
func TestDynamicActionGeneration(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	taskID := uuid.New().String()

	// T0: 初始规划生成 A1, A2（并行任务）
	a1ID := createTestAction(ctx, t, store, taskID)
	createTestAction(ctx, t, store, taskID)

	// 验证初始状态：两个 action 都是 open
	actions := listOpenActions(ctx, t, store, taskID)
	if len(actions) != 2 {
		t.Fatalf("期望 2 个 open actions，实际 %d", len(actions))
	}

	// T1: 执行 A1 并完成
	updateActionState(ctx, t, store, a1ID, string(explorationgraph.StateRunning))
	updateActionState(ctx, t, store, a1ID, string(explorationgraph.StateCompleted))

	// T2: Planner 看到 A1 的观察结果后，下一轮再提出后续动作 A3
	createTestAction(ctx, t, store, taskID)

	// T3: 待执行列表 = 未执行的 A2 + 新加入的 A3（已完成的 A1 不在其中）
	actions = listOpenActions(ctx, t, store, taskID)
	if len(actions) != 2 {
		t.Fatalf("期望 2 个 open actions (A2, A3)，实际 %d", len(actions))
	}
	for _, a := range actions {
		if a.ID == a1ID {
			t.Fatal("已完成的 A1 不应出现在待执行列表")
		}
	}
}

// TestConcurrentActionClaim 验证 CAS 并发抢占
//
// 场景：多个 runner 实例并发抢占同一个 Action
func TestConcurrentActionClaim(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	taskID := uuid.New().String()

	// 创建一个 open action
	actionID := createTestAction(ctx, t, store, taskID)

	// 模拟两个 runner 实例并发抢占
	ch := make(chan bool, 2)

	// 实例 1：尝试抢占
	go func() {
		success, err := store.CompareAndSwapActionState(ctx, taskID, actionID,
			explorationgraph.StateOpen, explorationgraph.StateRunning)
		if err != nil {
			t.Errorf("实例1 CAS 失败: %v", err)
			ch <- false
			return
		}
		ch <- success
	}()

	// 实例 2：尝试抢占（应该失败）
	go func() {
		// 确保实例 1 先执行
		time.Sleep(10 * time.Millisecond)
		success, err := store.CompareAndSwapActionState(ctx, taskID, actionID,
			explorationgraph.StateOpen, explorationgraph.StateRunning)
		if err != nil {
			t.Errorf("实例2 CAS 失败: %v", err)
			ch <- false
			return
		}
		ch <- success
	}()

	// 收集结果
	result1 := <-ch
	result2 := <-ch

	// 验证：只有一个实例成功抢占
	if result1 == result2 {
		t.Error("CAS 并发控制失败：两个实例的结果应该不同")
	}

	successCount := 0
	if result1 {
		successCount++
	}
	if result2 {
		successCount++
	}

	if successCount != 1 {
		t.Errorf("期望恰好 1 个实例抢占成功，实际 %d", successCount)
	}
}

// === 辅助函数 ===

func setupTestStore(t *testing.T) *explorationgraph.Store {
	t.Helper()
	return explorationgraph.NewMemoryStore()
}

func createTestAction(ctx context.Context, t *testing.T, store *explorationgraph.Store, taskID string) string {
	actionID := uuid.New().String()
	state := explorationgraph.StateOpen

	node := explorationgraph.Node{
		ID:        actionID,
		TaskID:    taskID,
		Kind:      core.KindAction,
		State:     &state,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if _, err := store.CreateNode(ctx, node); err != nil {
		t.Fatalf("创建 action %s: %v", actionID, err)
	}
	return actionID
}

func listOpenActions(ctx context.Context, t *testing.T, store *explorationgraph.Store, taskID string) []explorationgraph.Node {
	t.Helper()
	nodes, err := store.ListOpenActions(ctx, taskID)
	if err != nil {
		t.Fatalf("ListOpenActions: %v", err)
	}
	return nodes
}

func updateActionState(ctx context.Context, t *testing.T, store *explorationgraph.Store, actionID string, newState string) {
	t.Helper()
	if err := store.TransitionState(ctx, actionID, explorationgraph.State(newState), nil); err != nil {
		t.Fatalf("更新 action %s 状态: %v", actionID, err)
	}
}
