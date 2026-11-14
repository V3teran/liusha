package monitor

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// replanRecorder 记录收到的重规划请求（规划者的替身）。
type replanRecorder struct{ reasons []string }

func (r *replanRecorder) RequestReplan(reason string) { r.reasons = append(r.reasons, reason) }

// request_replan 直接交给规划者：控制信号不走可丢的事件总线，也不写进图——
// 它是给规划者的一次性指令，不是图上的事实。
func TestPublishDecision_RequestReplanReachesPlanner(t *testing.T) {
	ctx := context.Background()
	graph := explorationgraph.NewMemoryStore()
	taskID := "test-task"
	objID, err := graph.CreateNode(ctx, explorationgraph.Node{
		ID:      "tools_replan-1",
		TaskID:  taskID,
		Kind:    core.KindObjective,
		Content: []byte(`{"description":"test objective"}`),
	})
	require.NoError(t, err)

	planner := &replanRecorder{}
	tool := NewPublishDecisionTool(graph, planner, taskID)

	for _, reason := range []string{"探索停滞 1 小时无 result", "ROI 过低"} {
		args, _ := json.Marshal(Decision{Type: "request_replan", Reason: reason})
		result, err := tool.Execute(ctx, args)
		require.NoError(t, err)
		assert.Empty(t, result.Error)
		var out map[string]interface{}
		require.NoError(t, json.Unmarshal([]byte(result.Output), &out))
		assert.Equal(t, true, out["applied"])
	}
	assert.Equal(t, []string{"探索停滞 1 小时无 result", "ROI 过低"}, planner.reasons)

	node, err := graph.GetNode(ctx, objID)
	require.NoError(t, err)
	assert.JSONEq(t, `{"description":"test objective"}`, string(node.Content), "重规划请求不写进图")
}

// TestPublishDecision_KillActionStillWorks 验证 kill_action 不受影响
func TestPublishDecision_KillActionStillWorks(t *testing.T) {
	ctx := context.Background()
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
	tool := NewPublishDecisionTool(graph, &replanRecorder{}, taskID)

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
	graph := explorationgraph.NewMemoryStore()

	tool := NewPublishDecisionTool(graph, &replanRecorder{}, "test-task")

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

// kill_action 只能中止本任务的 action：其他任务的节点、本任务的非 action 节点都拒绝且不改状态。
func TestPublishDecision_KillActionRejectsForeignOrNonAction(t *testing.T) {
	ctx := context.Background()
	graph := explorationgraph.NewMemoryStore()
	stateOpen := explorationgraph.StateOpen
	mk := func(task string, kind core.NodeKind) string {
		id := "n-" + uuid.New().String()
		_, err := graph.CreateNode(ctx, explorationgraph.Node{ID: id, TaskID: task, Kind: kind, State: &stateOpen, Content: []byte(`{}`)})
		require.NoError(t, err)
		return id
	}
	foreign := mk("other-task", core.KindAction)
	objective := mk("my-task", core.KindObjective)
	tool := NewPublishDecisionTool(graph, &replanRecorder{}, "my-task")

	for _, id := range []string{foreign, objective} {
		args, _ := json.Marshal(Decision{Type: "kill_action", ActionID: id, Reason: "x"})
		res, err := tool.Execute(ctx, args)
		require.NoError(t, err)
		assert.NotEmpty(t, res.Error, "应拒绝 %s", id)
		node, err := graph.GetNode(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, explorationgraph.StateOpen, *node.State, "被拒节点状态不得改变")
	}
}
