package monitor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/bus"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// TestPublishDecision_RequestReplanPublishesEvent 验证 P2 修复：
// request_replan 发布事件并写入图
func TestPublishDecision_RequestReplanPublishesEvent(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()

	taskID := "test-task"

	// 创建 objective
	_, err := graph.CreateNode(ctx, explorationgraph.Node{
		TaskID:  taskID,
		Kind:    core.KindObjective,
		Content: []byte(`{"description":"test objective"}`),
	})
	require.NoError(t, err)

	// 创建工具
	tool := NewPublishDecisionTool(graph, eventBus, taskID)

	// 订阅事件
	sub := eventBus.SubscribeTask(taskID)
	defer sub.Cancel()

	// 调用 request_replan
	decision := Decision{
		Type:   "request_replan",
		Reason: "探索停滞 1 小时无 result",
	}
	args, _ := json.Marshal(decision)

	result, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.Empty(t, result.Error, "不应有错误")

	// 验证返回结果
	var resultData map[string]interface{}
	err = json.Unmarshal([]byte(result.Output), &resultData)
	require.NoError(t, err)
	assert.Equal(t, "request_replan", resultData["type"])
	assert.Equal(t, true, resultData["applied"], "applied 应为 true")

	// 验证事件发布
	select {
	case event := <-sub.Events():
		assert.Equal(t, bus.EventReplanRequested, event.Type, "应发布 EventReplanRequested")
		assert.Equal(t, decision.Reason, event.Payload["reason"], "reason 应传递")
		t.Log("✅ EventReplanRequested 事件已发布")
	case <-time.After(100 * time.Millisecond):
		t.Fatal("超时：未收到 EventReplanRequested 事件")
	}

	t.Log("✅ request_replan 发布事件成功")
}

// TestPublishDecision_RequestReplanWritesToGraph 验证写入图的 metadata
func TestPublishDecision_RequestReplanWritesToGraph(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()

	taskID := "test-task"

	// 创建 objective
	objID, err := graph.CreateNode(ctx, explorationgraph.Node{
		TaskID:  taskID,
		Kind:    core.KindObjective,
		Content: []byte(`{"description":"test objective"}`),
	})
	require.NoError(t, err)

	// 创建工具
	tool := NewPublishDecisionTool(graph, eventBus, taskID)

	// 调用 request_replan
	decision := Decision{
		Type:   "request_replan",
		Reason: "资源耗尽：ROI <0.1",
	}
	args, _ := json.Marshal(decision)

	_, err = tool.Execute(ctx, args)
	require.NoError(t, err)

	// 验证 metadata 写入
	node, err := graph.GetNode(ctx, objID)
	require.NoError(t, err)

	var content map[string]interface{}
	err = json.Unmarshal(node.Content, &content)
	require.NoError(t, err)

	// 检查 monitor_request
	require.Contains(t, content, "monitor_request", "应包含 monitor_request")
	monitorReq := content["monitor_request"].(map[string]interface{})

	require.Contains(t, monitorReq, "monitor_replan_request")
	replanReq := monitorReq["monitor_replan_request"].(map[string]interface{})

	assert.Equal(t, decision.Reason, replanReq["reason"], "reason 应写入")
	assert.Contains(t, replanReq, "requested_at", "应包含时间戳")

	t.Log("✅ request_replan 写入图的 metadata 成功")
}

// TestPublishDecision_KillActionStillWorks 验证 kill_action 不受影响
func TestPublishDecision_KillActionStillWorks(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()

	taskID := "test-task"

	// 创建 action（必须显式设置 ID）
	actionID := "action-" + uuid.New().String()
	stateOpen := explorationgraph.StateOpen
	_, err := graph.CreateNode(ctx, explorationgraph.Node{
		ID:      actionID,
		TaskID:  taskID,
		Kind:    core.KindAction,
		State:   &stateOpen,
		Content: []byte(`{"instruction":"test"}`),
	})
	require.NoError(t, err)

	// 创建工具
	tool := NewPublishDecisionTool(graph, eventBus, taskID)

	// 调用 kill_action
	decision := Decision{
		Type:     "kill_action",
		ActionID: actionID,
		Reason:   "卡住 30 分钟",
	}
	args, _ := json.Marshal(decision)

	result, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.Empty(t, result.Error)

	// 验证状态为 aborted
	node, err := graph.GetNode(ctx, actionID)
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateAborted, *node.State)

	t.Log("✅ kill_action 功能不受 P2 修复影响")
}

// TestPublishDecision_InvalidDecisionType 验证参数验证
func TestPublishDecision_InvalidDecisionType(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()

	tool := NewPublishDecisionTool(graph, eventBus, "test-task")

	// 无效的 decision type
	decision := Decision{
		Type:   "invalid_type",
		Reason: "test",
	}
	args, _ := json.Marshal(decision)

	result, err := tool.Execute(ctx, args)
	require.NoError(t, err)
	assert.NotEmpty(t, result.Error, "应返回错误")
	assert.Contains(t, result.Error, "type must be", "错误消息应说明类型无效")

	t.Log("✅ 参数验证正常")
}

// TestPublishDecision_MultipleReplanRequests 验证多次 replan 请求
func TestPublishDecision_MultipleReplanRequests(t *testing.T) {
	ctx := context.Background()
	eventBus := bus.New(ctx)
	graph := explorationgraph.NewMemoryStore()

	taskID := "test-task"

	// 创建 objective
	objID, err := graph.CreateNode(ctx, explorationgraph.Node{
		TaskID:  taskID,
		Kind:    core.KindObjective,
		Content: []byte(`{"description":"test"}`),
	})
	require.NoError(t, err)

	tool := NewPublishDecisionTool(graph, eventBus, taskID)

	// 第一次 replan
	decision1 := Decision{
		Type:   "request_replan",
		Reason: "停滞 1 小时",
	}
	args1, _ := json.Marshal(decision1)
	_, err = tool.Execute(ctx, args1)
	require.NoError(t, err)

	// 第二次 replan（应覆盖）
	decision2 := Decision{
		Type:   "request_replan",
		Reason: "ROI 过低",
	}
	args2, _ := json.Marshal(decision2)
	_, err = tool.Execute(ctx, args2)
	require.NoError(t, err)

	// 验证只保留最新的
	node, err := graph.GetNode(ctx, objID)
	require.NoError(t, err)

	var content map[string]interface{}
	json.Unmarshal(node.Content, &content)

	monitorReq := content["monitor_request"].(map[string]interface{})
	replanReq := monitorReq["monitor_replan_request"].(map[string]interface{})

	assert.Equal(t, decision2.Reason, replanReq["reason"], "应保留最新的 reason")

	t.Log("✅ 多次 replan 请求处理正确（保留最新）")
}
