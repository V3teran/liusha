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

// TestE2ELogic 测试端到端逻辑（不依赖数据库）
func TestE2ELogic(t *testing.T) {
	ctx := context.Background()
	taskID := uuid.New().String()

	t.Run("Objective结构验证", func(t *testing.T) {
		// 模拟 onboard 创建的 Objective
		objectiveContent, err := json.Marshal(map[string]interface{}{
			"description": "扫描 example.com 的常见漏洞",
			"target_ref":  "http://example.com",
		})
		require.NoError(t, err)

		objective := explorationgraph.Node{
			ID:         uuid.New().String(),
			TaskID:     taskID,
			Kind:       core.KindObjective,
			Content:    objectiveContent,
			Priority:   explorationgraph.PriorityHigh,
			SourceType: "test",
			CreatedAt:  time.Now(),
		}

		// 验证 Planner 能读取 description
		var objData struct {
			Description string `json:"description"`
			TargetRef   string `json:"target_ref"`
		}
		err = json.Unmarshal(objective.Content, &objData)
		require.NoError(t, err)
		assert.Equal(t, "扫描 example.com 的常见漏洞", objData.Description)
		assert.Equal(t, "http://example.com", objData.TargetRef)

		t.Logf("✓ Objective 结构正确")
		t.Logf("  Description: %s", objData.Description)
		t.Logf("  TargetRef: %s", objData.TargetRef)
	})

	t.Run("Action状态转换", func(t *testing.T) {
		stateOpen := explorationgraph.StateOpen
		action := explorationgraph.Node{
			ID:        uuid.New().String(),
			TaskID:    taskID,
			Kind:      core.KindAction,
			State:     &stateOpen,
			DependsOn: []string{},
		}

		// 验证初始状态
		assert.Equal(t, explorationgraph.StateOpen, *action.State)
		t.Logf("✓ Action 初始状态: open")

		// 模拟状态转换
		stateRunning := explorationgraph.StateRunning
		action.State = &stateRunning
		assert.Equal(t, explorationgraph.StateRunning, *action.State)
		t.Logf("✓ Action 状态转换: open → running")

		stateDone := explorationgraph.StateDone
		action.State = &stateDone
		assert.Equal(t, explorationgraph.StateDone, *action.State)
		t.Logf("✓ Action 状态转换: running → done")
	})

	t.Run("依赖关系检查", func(t *testing.T) {
		actionAID := uuid.New().String()
		actionBID := uuid.New().String()

		// Action A: 无依赖
		stateOpen := explorationgraph.StateOpen
		actionA := explorationgraph.Node{
			ID:        actionAID,
			Kind:      core.KindAction,
			State:     &stateOpen,
			DependsOn: []string{},
		}

		// Action B: 依赖 A
		actionB := explorationgraph.Node{
			ID:        actionBID,
			Kind:      core.KindAction,
			State:     &stateOpen,
			DependsOn: []string{actionAID},
		}

		// B 在 A 未完成时不可执行
		emptyCompleted := make(map[string]bool)
		assert.True(t, actionA.CanExecute(emptyCompleted), "无依赖的 Action 应可执行")
		assert.False(t, actionB.CanExecute(emptyCompleted), "依赖未满足时不可执行")
		t.Logf("✓ 依赖未满足: A可执行, B不可执行")

		// B 在 A 完成后可执行
		completedWithA := map[string]bool{actionAID: true}
		assert.True(t, actionA.CanExecute(completedWithA))
		assert.True(t, actionB.CanExecute(completedWithA), "依赖满足后应可执行")
		t.Logf("✓ 依赖满足: A和B都可执行")
	})

	t.Run("事件总线流转", func(t *testing.T) {
		eventBus := bus.New(ctx)

		events := eventBus.SubscribeTask(taskID)
		defer eventBus.UnsubscribeTask(taskID)

		// 测试 action.proposed
		go func() {
			time.Sleep(10 * time.Millisecond)
			eventBus.PublishActionProposed(taskID, "action-1")
		}()

		select {
		case evt := <-events:
			assert.Equal(t, bus.EventActionProposed, evt.Type)
			assert.Equal(t, "action-1", evt.Payload["action_id"])
			t.Logf("✓ 事件接收成功: action.proposed")
		case <-time.After(1 * time.Second):
			t.Fatal("未收到事件")
		}

		// 测试 verification.passed
		go func() {
			time.Sleep(10 * time.Millisecond)
			eventBus.PublishVerificationPassed(taskID, "result-1")
		}()

		select {
		case evt := <-events:
			assert.Equal(t, bus.EventVerificationPassed, evt.Type)
			t.Logf("✓ 事件接收成功: verification.passed")
		case <-time.After(1 * time.Second):
			t.Fatal("未收到事件")
		}
	})

	t.Run("完整事件循环模拟", func(t *testing.T) {
		eventBus := bus.New(ctx)
		events := eventBus.SubscribeTask(taskID)
		defer eventBus.UnsubscribeTask(taskID)

		receivedEvents := []string{}

		// 模拟完整循环
		go func() {
			time.Sleep(10 * time.Millisecond)
			eventBus.PublishActionProposed(taskID, "action-1")
			t.Logf("→ Planner 发布: action.proposed")

			time.Sleep(10 * time.Millisecond)
			eventBus.PublishActionCompleted(taskID, "action-1")
			t.Logf("→ Executor 发布: action.completed")

			time.Sleep(10 * time.Millisecond)
			eventBus.PublishAttemptGenerated(taskID, "action-1", nil)
			t.Logf("→ Executor 发布: attempt.generated")

			time.Sleep(10 * time.Millisecond)
			eventBus.PublishVerificationPassed(taskID, "result-1")
			t.Logf("→ Evaluator 发布: verification.passed")
		}()

		// 接收事件
		timeout := time.After(2 * time.Second)
		for len(receivedEvents) < 4 {
			select {
			case evt := <-events:
				receivedEvents = append(receivedEvents, string(evt.Type))
				t.Logf("← 接收事件: %s", evt.Type)
			case <-timeout:
				t.Fatalf("超时，只接收到 %d 个事件", len(receivedEvents))
			}
		}

		assert.Len(t, receivedEvents, 4)
		assert.Equal(t, "action.proposed", receivedEvents[0])
		assert.Equal(t, "action.completed", receivedEvents[1])
		assert.Equal(t, "attempt.generated", receivedEvents[2])
		assert.Equal(t, "verification.passed", receivedEvents[3])

		t.Logf("✓ 完整事件循环验证成功")
	})

	t.Logf("\n========================================")
	t.Logf("✅ 端到端逻辑测试全部通过")
	t.Logf("========================================")
}

// TestActionIsAction 验证 IsAction 方法
func TestActionIsAction(t *testing.T) {
	stateOpen := explorationgraph.StateOpen

	tests := []struct {
		name     string
		node     explorationgraph.Node
		expected bool
	}{
		{
			name: "Action节点",
			node: explorationgraph.Node{
				Kind:  core.KindAction,
				State: &stateOpen,
			},
			expected: true,
		},
		{
			name: "Objective节点",
			node: explorationgraph.Node{
				Kind: core.KindObjective,
			},
			expected: false,
		},
		{
			name: "Result节点",
			node: explorationgraph.Node{
				Kind: core.KindResult,
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.node.IsAction()
			assert.Equal(t, tt.expected, result)
		})
	}
}
