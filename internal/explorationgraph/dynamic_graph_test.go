package explorationgraph_test

import (
	"context"
	"encoding/json"
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
	a1ID := createTestAction(t, ctx, store, taskID, "A1", nil)
	a2ID := createTestAction(t, ctx, store, taskID, "A2", nil)

	// 验证初始状态：两个 action 都是 open
	actions := listOpenActions(t, ctx, store, taskID)
	if len(actions) != 2 {
		t.Fatalf("期望 2 个 open actions，实际 %d", len(actions))
	}

	// T1: 执行 A1 并完成
	updateActionState(t, ctx, store, a1ID, string(explorationgraph.StateRunning))
	updateActionState(t, ctx, store, a1ID, string(explorationgraph.StateDone))

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
	updateActionState(t, ctx, store, a2ID, string(explorationgraph.StateRunning))
	updateActionState(t, ctx, store, a2ID, string(explorationgraph.StateDone))
	updateActionState(t, ctx, store, a3ID, string(explorationgraph.StateRunning))
	updateActionState(t, ctx, store, a3ID, string(explorationgraph.StateDone))

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
		success, err := store.CompareAndSwapActionState(ctx, taskID, actionID,
			explorationgraph.StateOpen, explorationgraph.StateRunning, nil)
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
			explorationgraph.StateOpen, explorationgraph.StateRunning, nil)
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
	updateActionState(t, ctx, store, a1ID, string(explorationgraph.StateRunning))
	updateActionState(t, ctx, store, a1ID, string(explorationgraph.StateDone))
	completed[a1ID] = true

	// 验证 A2 现在可执行（依赖已满足）
	if !isDependencySatisfied(a2, completed) {
		t.Error("A2 现在应该可执行（依赖 A1 已完成）")
	}
}

// === 辅助函数 ===

func setupTestStore(t *testing.T) *explorationgraph.Store {
	t.Helper()
	return explorationgraph.NewMemoryStore()
}

func createTestAction(t *testing.T, ctx context.Context, store *explorationgraph.AdapterStore, taskID, name string, dependsOn []string) string {
	actionID := uuid.New().String()
	state := explorationgraph.StateOpen

	node := explorationgraph.Node{
		ID:        actionID,
		TaskID:    taskID,
		Kind:      core.KindAction,
		State:     &state,
		DependsOn: dependsOn,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	if _, err := store.CreateNode(ctx, node); err != nil {
		t.Fatalf("创建 action %s: %v", actionID, err)
	}
	return actionID
}

func listOpenActions(t *testing.T, ctx context.Context, store *explorationgraph.Store, taskID string) []explorationgraph.Node {
	t.Helper()
	nodes, err := store.ListOpenActions(ctx, taskID)
	if err != nil {
		t.Fatalf("ListOpenActions: %v", err)
	}
	return nodes
}

func updateActionState(t *testing.T, ctx context.Context, store *explorationgraph.Store, actionID string, newState string) {
	t.Helper()
	if err := store.UpdateActionStateWithReason(ctx, actionID, explorationgraph.State(newState), nil); err != nil {
		t.Fatalf("更新 action %s 状态: %v", actionID, err)
	}
}

func getActionByID(t *testing.T, ctx context.Context, store *explorationgraph.Store, actionID string) explorationgraph.Node {
	t.Helper()
	node, err := store.GetNode(ctx, actionID)
	if err != nil {
		t.Fatalf("GetNode %s: %v", actionID, err)
	}
	return *node
}

func updateActionDependencies(t *testing.T, ctx context.Context, store *explorationgraph.Store, actionID string, dependsOn []string) {
	t.Helper()
	meta, err := json.Marshal(map[string]interface{}{"depends_on": dependsOn})
	if err != nil {
		t.Fatalf("marshal depends_on: %v", err)
	}
	if err := store.UpdateNodeMetadata(ctx, actionID, meta); err != nil {
		t.Fatalf("更新依赖: %v", err)
	}
}

func isDependencySatisfied(action explorationgraph.Node, completed map[string]bool) bool {
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
