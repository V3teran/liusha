package knowledgegraph_test

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/knowledgegraph"
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
	a1ID := createTestAction(t, ctx, store, taskID, "A1", nil)
	a2ID := createTestAction(t, ctx, store, taskID, "A2", nil)

	// 验证初始状态：两个 action 都是 open
	actions := listOpenActions(t, ctx, store, taskID)
	if len(actions) != 2 {
		t.Fatalf("期望 2 个 open actions，实际 %d", len(actions))
	}

	// T1: 执行 A1 并完成
	updateActionState(t, ctx, store, a1ID, string(knowledgegraph.StateRunning))
	updateActionState(t, ctx, store, a1ID, string(knowledgegraph.StateDone))

	// T2: Planner 基于 A1 的观察结果动态生成新 Action A3（依赖 A1）
	a3ID := createTestAction(t, ctx, store, taskID, "A3", []string{a1ID})

	// T3: 验证动态图状态
	// - A1 已完成
	// - A2 仍然 open（未执行）
	// - A3 新加入，依赖 A1（已满足）
	actions = listOpenActions(t, ctx, store, taskID)
	if len(actions) != 2 {
		t.Fatalf("期望 2 个 open actions (A2, A3)，实际 %d", len(actions))
	}

	// 验证 A3 依赖已满足
	completed := map[string]bool{a1ID: true}
	a3 := getActionByID(t, ctx, store, a3ID)
	if !isDependencySatisfied(a3, completed) {
		t.Error("A3 的依赖应该已满足（A1 已完成）")
	}

	// T4: 继续动态生成 A4（依赖 A2 和 A3）
	a4ID := createTestAction(t, ctx, store, taskID, "A4", []string{a2ID, a3ID})

	// 验证 A4 依赖未满足（A2 和 A3 都未完成）
	a4 := getActionByID(t, ctx, store, a4ID)
	if isDependencySatisfied(a4, completed) {
		t.Error("A4 的依赖不应该满足（A2、A3 未完成）")
	}

	// T5: 执行并完成 A2 和 A3
	updateActionState(t, ctx, store, a2ID, string(knowledgegraph.StateRunning))
	updateActionState(t, ctx, store, a2ID, string(knowledgegraph.StateDone))
	updateActionState(t, ctx, store, a3ID, string(knowledgegraph.StateRunning))
	updateActionState(t, ctx, store, a3ID, string(knowledgegraph.StateDone))

	// 验证 A4 依赖现在已满足
	completed[a2ID] = true
	completed[a3ID] = true
	a4 = getActionByID(t, ctx, store, a4ID)
	if !isDependencySatisfied(a4, completed) {
		t.Error("A4 的依赖应该已满足（A2、A3 已完成）")
	}
}

// TestConcurrentActionClaim 验证 CAS 并发抢占
//
// 场景：多个 Orchestrator 实例并发抢占同一个 Action
func TestConcurrentActionClaim(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	taskID := uuid.New().String()

	// 创建一个 open action
	actionID := createTestAction(t, ctx, store, taskID, "A1", nil)

	// 模拟两个 Orchestrator 实例并发抢占
	ch := make(chan bool, 2)

	// 实例 1：尝试抢占
	go func() {
		success, err := store.CompareAndSwapState(ctx, taskID, actionID,
			string(knowledgegraph.StateOpen), string(knowledgegraph.StateRunning))
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
		success, err := store.CompareAndSwapState(ctx, taskID, actionID,
			string(knowledgegraph.StateOpen), string(knowledgegraph.StateRunning))
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

// TestDynamicDependencyResolution 验证动态依赖解析
//
// 场景：运行时添加新依赖关系
func TestDynamicDependencyResolution(t *testing.T) {
	ctx := context.Background()
	store := setupTestStore(t)
	taskID := uuid.New().String()

	// 创建独立的 action A1（无依赖）
	a1ID := createTestAction(t, ctx, store, taskID, "A1", nil)

	// 创建 action A2（初始无依赖）
	a2ID := createTestAction(t, ctx, store, taskID, "A2", nil)

	// 验证 A2 初始可执行
	completed := map[string]bool{}
	a2 := getActionByID(t, ctx, store, a2ID)
	if !isDependencySatisfied(a2, completed) {
		t.Error("A2 初始应该可执行（无依赖）")
	}

	// 动态修改：A2 现在依赖 A1
	updateActionDependencies(t, ctx, store, a2ID, []string{a1ID})

	// 重新读取 A2
	a2 = getActionByID(t, ctx, store, a2ID)

	// 验证 A2 现在不可执行（A1 未完成）
	if isDependencySatisfied(a2, completed) {
		t.Error("A2 现在不应该可执行（依赖 A1 未完成）")
	}

	// 完成 A1
	updateActionState(t, ctx, store, a1ID, string(knowledgegraph.StateRunning))
	updateActionState(t, ctx, store, a1ID, string(knowledgegraph.StateDone))
	completed[a1ID] = true

	// 验证 A2 现在可执行（依赖已满足）
	if !isDependencySatisfied(a2, completed) {
		t.Error("A2 现在应该可执行（依赖 A1 已完成）")
	}
}

// === 辅助函数 ===

func setupTestStore(t *testing.T) *knowledgegraph.Store {
	// TODO: 实际实现应该使用 PostgreSQL 测试容器或内存实现
	// 当前只是框架，待 core.GraphStore 接口稳定后实现
	t.Skip("需要 GraphStore 测试实现")
	return nil
}

func createTestAction(t *testing.T, ctx context.Context, store *knowledgegraph.AdapterStore, taskID, name string, dependsOn []string) string {
	actionID := uuid.New().String()
	state := knowledgegraph.StateOpen

	node := knowledgegraph.Node{
		ID:        actionID,
		TaskID:    taskID,
		Kind:      core.KindAction,
		State:     &state,
		DependsOn: dependsOn,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	// TODO: 调用 store.CreateNode
	_ = node
	return actionID
}

func listOpenActions(t *testing.T, ctx context.Context, store *knowledgegraph.Store, taskID string) []knowledgegraph.Node {
	// TODO: 调用 store.ListOpenActions
	return nil
}

func updateActionState(t *testing.T, ctx context.Context, store *knowledgegraph.AdapterStore, actionID string, newState string) {
	// TODO: 调用 store.CompareAndSwapState 或 store.UpdateNode
}

func getActionByID(t *testing.T, ctx context.Context, store *knowledgegraph.Store, actionID string) knowledgegraph.Node {
	// TODO: 调用 store.GetNode
	return knowledgegraph.Node{}
}

func updateActionDependencies(t *testing.T, ctx context.Context, store *knowledgegraph.Store, actionID string, dependsOn []string) {
	// TODO: 调用 store.UpdateNode 修改 DependsOn 字段
}

func isDependencySatisfied(action knowledgegraph.Node, completed map[string]bool) bool {
	if len(action.DependsOn) == 0 {
		return true
	}
	for _, depID := range action.DependsOn {
		if !completed[depID] {
			return false
		}
	}
	return true
}
