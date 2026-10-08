package explorationgraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// TestUpdateActionStateWithReason_CannotOverwriteAborted 验证 P1-B 根治方案：
// UpdateActionStateWithReason 在架构层面防止覆盖 aborted 状态
func TestUpdateActionStateWithReason_CannotOverwriteAborted(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	// 创建 action
	taskID := "test-task"
	stateOpen := StateOpen
	action, err := graph.CreateNode(ctx, Node{
		TaskID: taskID,
		Kind:   core.KindAction,
		State:  &stateOpen,
		Content: []byte(`{"instruction":"test action"}`),
	})
	require.NoError(t, err)

	// 设置为 aborted
	reason := "monitor killed this action"
	err = graph.UpdateActionStateWithReason(ctx, action, StateAborted, &reason)
	require.NoError(t, err)

	// 验证状态为 aborted
	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	require.NotNil(t, node.State)
	assert.Equal(t, StateAborted, *node.State)

	// 尝试覆盖为 done（应该失败）
	err = graph.UpdateActionStateWithReason(ctx, action, StateDone, nil)
	assert.Error(t, err, "不应允许覆盖 aborted 状态")
	assert.Contains(t, err.Error(), "cannot overwrite aborted state", "错误消息应明确说明")

	// 验证状态仍为 aborted
	node, err = graph.GetNode(ctx, action)
	require.NoError(t, err)
	require.NotNil(t, node.State)
	assert.Equal(t, StateAborted, *node.State, "状态应保持 aborted")
	assert.Equal(t, reason, *node.BlockedReason, "kill reason 应保留")

	t.Log("✅ aborted 状态不可被覆盖为 done")
}

// TestUpdateActionStateWithReason_CannotOverwriteAbortedWithFailed 验证失败也不能覆盖
func TestUpdateActionStateWithReason_CannotOverwriteAbortedWithFailed(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	taskID := "test-task"
	stateOpen := StateOpen
	action, err := graph.CreateNode(ctx, Node{
		TaskID: taskID,
		Kind:   core.KindAction,
		State:  &stateOpen,
		Content: []byte(`{"instruction":"test"}`),
	})
	require.NoError(t, err)

	// 设置为 aborted
	killReason := "monitor killed"
	err = graph.UpdateActionStateWithReason(ctx, action, StateAborted, &killReason)
	require.NoError(t, err)

	// 尝试覆盖为 failed（应该失败）
	failReason := "execution failed"
	err = graph.UpdateActionStateWithReason(ctx, action, StateFailed, &failReason)
	assert.Error(t, err, "不应允许覆盖 aborted 状态")

	// 验证状态仍为 aborted
	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	require.NotNil(t, node.State)
	assert.Equal(t, StateAborted, *node.State)
	assert.Equal(t, killReason, *node.BlockedReason, "应保留原始 kill reason")

	t.Log("✅ aborted 状态不可被覆盖为 failed")
}

// TestUpdateActionStateWithReason_NormalTransitions 验证正常状态转换不受影响
func TestUpdateActionStateWithReason_NormalTransitions(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	taskID := "test-task"
	stateOpen := StateOpen
	action, err := graph.CreateNode(ctx, Node{
		TaskID: taskID,
		Kind:   core.KindAction,
		State:  &stateOpen,
		Content: []byte(`{"instruction":"test"}`),
	})
	require.NoError(t, err)

	// 正常转换：open → running
	err = graph.UpdateActionStateWithReason(ctx, action, StateRunning, nil)
	assert.NoError(t, err, "open → running 应成功")

	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, *node.State)

	// 正常转换：running → done
	err = graph.UpdateActionStateWithReason(ctx, action, StateDone, nil)
	assert.NoError(t, err, "running → done 应成功")

	node, err = graph.GetNode(ctx, action)
	require.NoError(t, err)
	assert.Equal(t, StateDone, *node.State)

	t.Log("✅ 正常状态转换不受影响")
}

// TestUpdateActionStateWithReason_AbortedToAborted 验证 aborted → aborted 允许（幂等）
func TestUpdateActionStateWithReason_AbortedToAborted(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	taskID := "test-task"
	stateOpen := StateOpen
	action, err := graph.CreateNode(ctx, Node{
		TaskID: taskID,
		Kind:   core.KindAction,
		State:  &stateOpen,
		Content: []byte(`{"instruction":"test"}`),
	})
	require.NoError(t, err)

	// 设置为 aborted
	reason1 := "first kill"
	err = graph.UpdateActionStateWithReason(ctx, action, StateAborted, &reason1)
	require.NoError(t, err)

	// 再次设置为 aborted（应该允许，幂等操作）
	reason2 := "second kill (redundant)"
	err = graph.UpdateActionStateWithReason(ctx, action, StateAborted, &reason2)
	assert.NoError(t, err, "aborted → aborted 应允许（幂等）")

	// 验证 reason 被更新
	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	assert.Equal(t, StateAborted, *node.State)
	assert.Equal(t, reason2, *node.BlockedReason, "reason 应更新为最新的")

	t.Log("✅ aborted → aborted 允许（幂等操作）")
}

// TestUpdateActionStateWithReason_ConcurrentKill 验证并发 kill 场景
func TestUpdateActionStateWithReason_ConcurrentKill(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	taskID := "test-task"
	stateRunning := StateRunning
	action, err := graph.CreateNode(ctx, Node{
		TaskID: taskID,
		Kind:   core.KindAction,
		State:  &stateRunning,
		Content: []byte(`{"instruction":"test"}`),
	})
	require.NoError(t, err)

	// 模拟并发场景：
	// 1. Monitor kill (aborted)
	// 2. Executor 完成 (done)

	// Monitor 先 kill
	killReason := "monitor killed"
	err = graph.UpdateActionStateWithReason(ctx, action, StateAborted, &killReason)
	require.NoError(t, err)

	// Executor 尝试标记 done（应该失败）
	err = graph.UpdateActionStateWithReason(ctx, action, StateDone, nil)
	assert.Error(t, err, "Executor 的 done 更新应被拒绝")

	// 验证最终状态为 aborted（Monitor 的决策获胜）
	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	assert.Equal(t, StateAborted, *node.State)
	assert.Equal(t, killReason, *node.BlockedReason)

	t.Log("✅ 并发场景下 Monitor 的 kill 决策获胜")
}

// TestUpdateActionStateWithReason_ErrorPropagation 验证错误传播
func TestUpdateActionStateWithReason_ErrorPropagation(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	// 尝试更新不存在的节点
	err := graph.UpdateActionStateWithReason(ctx, "non-existent", StateDone, nil)
	assert.Error(t, err, "更新不存在的节点应返回错误")

	t.Log("✅ 错误正确传播")
}
