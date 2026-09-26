package internal

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/explorationgraph"
)

// TestFourAgentIntegration 测试四Agent完整协作流程
func TestFourAgentIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	taskID := uuid.New().String()

	// 1. 创建内存存储和事件总线
	store := explorationgraph.NewMemoryStore()
	eventBus := bus.New(ctx)

	t.Logf("✓ 创建存储和事件总线")

	// 2. 创建 Objective 节点
	objectiveContent, _ := json.Marshal(map[string]interface{}{
		"description": "扫描 example.com 的常见漏洞",
		"target_ref":  "http://example.com",
	})

	objective := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindObjective,
		Content:    objectiveContent,
		Priority:   explorationgraph.PriorityHigh,
		SourceType: "test",
		SourceID:   "integration_test",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	_, err := store.CreateNode(ctx, objective)
	require.NoError(t, err, "创建 Objective 失败")
	t.Logf("✓ 创建 Objective: %s", objective.ID)

	// 3. 验证 Objective 可以被读取
	objectives, err := store.ListNodesByKind(ctx, taskID, core.KindObjective)
	require.NoError(t, err)
	require.Len(t, objectives, 1)

	var objData struct {
		Description string `json:"description"`
	}
	err = json.Unmarshal(objectives[0].Content, &objData)
	require.NoError(t, err)
	assert.Equal(t, "扫描 example.com 的常见漏洞", objData.Description)
	t.Logf("✓ Objective 读取成功: %s", objData.Description)

	// 4. 模拟 Planner 创建 Action
	actionContent, _ := json.Marshal(map[string]interface{}{
		"instruction": "扫描 SQL 注入漏洞",
		"complexity":  "simple",
	})

	stateOpen := explorationgraph.StateOpen
	action := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindAction,
		Content:    actionContent,
		State:      &stateOpen,
		Priority:   explorationgraph.PriorityHigh,
		DependsOn:  []string{}, // 无依赖
		SourceType: "planner",
		SourceID:   "intelligence",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	_, err = store.CreateNode(ctx, action)
	require.NoError(t, err)
	t.Logf("✓ Planner 创建 Action: %s", action.ID)

	// 5. 验证 Action 状态为 open
	openActions, err := store.ListOpenActions(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, openActions, 1)
	assert.Equal(t, action.ID, openActions[0].ID)
	t.Logf("✓ Action 状态为 open")

	// 6. 验证依赖检查
	completed := make(map[string]bool)
	canExecute := action.CanExecute(completed)
	assert.True(t, canExecute, "无依赖的 Action 应该可执行")
	t.Logf("✓ 依赖检查通过: CanExecute = true")

	// 7. 模拟 Executor 更新状态为 running
	err = store.UpdateActionStateWithReason(ctx, action.ID, explorationgraph.StateRunning, nil)
	require.NoError(t, err)
	t.Logf("✓ Executor 更新状态: open → running")

	// 8. 验证状态更新
	updatedAction, err := store.GetNode(ctx, action.ID)
	require.NoError(t, err)
	require.NotNil(t, updatedAction.State)
	assert.Equal(t, explorationgraph.StateRunning, *updatedAction.State)
	t.Logf("✓ 状态验证成功: running")

	// 9. 模拟 Executor 更新状态为 done
	err = store.UpdateActionStateWithReason(ctx, action.ID, explorationgraph.StateDone, nil)
	require.NoError(t, err)
	t.Logf("✓ Executor 更新状态: running → done")

	// 10. 验证 Action 不再是 open
	openActionsAfter, err := store.ListOpenActions(ctx, taskID)
	require.NoError(t, err)
	assert.Len(t, openActionsAfter, 0, "完成的 Action 不应出现在 open 列表")
	t.Logf("✓ Action 已从 open 列表移除")

	// 11. 验证 Action 在 completed 列表
	completedActions, err := store.ListCompletedActions(ctx, taskID)
	require.NoError(t, err)
	require.Len(t, completedActions, 1)
	assert.Equal(t, action.ID, completedActions[0].ID)
	t.Logf("✓ Action 出现在 completed 列表")

	// 12. 模拟 Evaluator 创建 Result
	resultContent, _ := json.Marshal(map[string]interface{}{
		"finding_id": "finding-123",
		"severity":   "high",
		"title":      "SQL 注入漏洞",
	})

	result := explorationgraph.Node{
		ID:         uuid.New().String(),
		TaskID:     taskID,
		Kind:       core.KindResult,
		Content:    resultContent,
		Priority:   explorationgraph.PriorityHigh,
		SourceType: "evaluator",
		SourceID:   "promoter",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	_, err = store.CreateNode(ctx, result)
	require.NoError(t, err)
	t.Logf("✓ Evaluator 创建 Result: %s", result.ID)

	// 13. 测试事件总线
	t.Run("事件流转", func(t *testing.T) {
		events := eventBus.SubscribeTask(taskID)

		// 发布 action.proposed
		eventBus.PublishActionProposed(taskID, action.ID)

		// 验证接收
		select {
		case evt := <-events:
			assert.Equal(t, bus.EventActionProposed, evt.Type)
			assert.Equal(t, action.ID, evt.Payload["action_id"])
			t.Logf("✓ 事件接收成功: action.proposed")
		case <-time.After(1 * time.Second):
			t.Fatal("未收到事件")
		}

		eventBus.UnsubscribeTask(taskID)
	})

	// 14. 测试依赖关系
	t.Run("依赖关系", func(t *testing.T) {
		// 创建依赖 Action
		action2Content, _ := json.Marshal(map[string]interface{}{
			"instruction": "深度扫描",
		})

		stateOpen2 := explorationgraph.StateOpen
		action2 := explorationgraph.Node{
			ID:         uuid.New().String(),
			TaskID:     taskID,
			Kind:       core.KindAction,
			Content:    action2Content,
			State:      &stateOpen2,
			DependsOn:  []string{action.ID}, // 依赖 action
			SourceType: "planner",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		}

		_, err := store.CreateNode(ctx, action2)
		require.NoError(t, err)

		// 依赖未满足时不可执行
		emptyCompleted := make(map[string]bool)
		assert.False(t, action2.CanExecute(emptyCompleted))
		t.Logf("✓ 依赖未满足: CanExecute = false")

		// 依赖满足后可执行
		fullCompleted := map[string]bool{action.ID: true}
		assert.True(t, action2.CanExecute(fullCompleted))
		t.Logf("✓ 依赖满足: CanExecute = true")
	})

	t.Logf("\n========================================")
	t.Logf("✅ 四Agent 集成测试全部通过")
	t.Logf("========================================")
}

// TestEventBusFlow 测试事件总线完整流转
func TestEventBusFlow(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	taskID := uuid.New().String()
	eventBus := bus.New(ctx)

	events := eventBus.SubscribeTask(taskID)
	defer eventBus.UnsubscribeTask(taskID)

	testCases := []struct {
		name      string
		publish   func()
		eventType bus.EventType
	}{
		{
			name: "action.proposed",
			publish: func() {
				eventBus.PublishActionProposed(taskID, "action-1")
			},
			eventType: bus.EventActionProposed,
		},
		{
			name: "action.completed",
			publish: func() {
				eventBus.PublishActionCompleted(taskID, "action-1")
			},
			eventType: bus.EventActionCompleted,
		},
		{
			name: "verification.passed",
			publish: func() {
				eventBus.PublishVerificationPassed(taskID, "result-1")
			},
			eventType: bus.EventVerificationPassed,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.publish()

			select {
			case evt := <-events:
				assert.Equal(t, tc.eventType, evt.Type)
				t.Logf("✓ 事件 %s 接收成功", tc.name)
			case <-time.After(1 * time.Second):
				t.Fatalf("未收到事件: %s", tc.name)
			}
		})
	}
}
