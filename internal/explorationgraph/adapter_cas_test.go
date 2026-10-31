package explorationgraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// TestTransitionState_CannotOverwriteAborted 验证 P1-B 根治方案：
// TransitionState 在架构层面防止覆盖 aborted 状态
func TestTransitionState_CannotOverwriteAborted(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	// 创建 action
	taskID := "test-task"
	stateOpen := StateOpen
	action, err := graph.CreateNode(ctx, Node{
		TaskID:  taskID,
		Kind:    core.KindAction,
		State:   &stateOpen,
		Content: []byte(`{"instruction":"test action"}`),
	})
	require.NoError(t, err)

	// 设置为 aborted
	reason := "monitor killed this action"
	err = graph.TransitionState(ctx, action, StateAborted, &reason)
	require.NoError(t, err)

	// 验证状态为 aborted
	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	require.NotNil(t, node.State)
	assert.Equal(t, StateAborted, *node.State)

	// 尝试覆盖为 done（应该失败）
	err = graph.TransitionState(ctx, action, StateCompleted, nil)
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

// TestTransitionState_CannotOverwriteAbortedWithFailed 验证失败也不能覆盖
func TestTransitionState_CannotOverwriteAbortedWithFailed(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	taskID := "test-task"
	stateOpen := StateOpen
	action, err := graph.CreateNode(ctx, Node{
		TaskID:  taskID,
		Kind:    core.KindAction,
		State:   &stateOpen,
		Content: []byte(`{"instruction":"test"}`),
	})
	require.NoError(t, err)

	// 设置为 aborted
	killReason := "monitor killed"
	err = graph.TransitionState(ctx, action, StateAborted, &killReason)
	require.NoError(t, err)

	// 尝试覆盖为 failed（应该失败）
	failReason := "execution failed"
	err = graph.TransitionState(ctx, action, StateFailed, &failReason)
	assert.Error(t, err, "不应允许覆盖 aborted 状态")

	// 验证状态仍为 aborted
	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	require.NotNil(t, node.State)
	assert.Equal(t, StateAborted, *node.State)
	assert.Equal(t, killReason, *node.BlockedReason, "应保留原始 kill reason")

	t.Log("✅ aborted 状态不可被覆盖为 failed")
}

// TestTransitionState_NormalTransitions 验证正常状态转换不受影响
func TestTransitionState_NormalTransitions(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	taskID := "test-task"
	stateOpen := StateOpen
	action, err := graph.CreateNode(ctx, Node{
		TaskID:  taskID,
		Kind:    core.KindAction,
		State:   &stateOpen,
		Content: []byte(`{"instruction":"test"}`),
	})
	require.NoError(t, err)

	// 正常转换：open → running
	err = graph.TransitionState(ctx, action, StateRunning, nil)
	assert.NoError(t, err, "open → running 应成功")

	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	assert.Equal(t, StateRunning, *node.State)

	// 正常转换：running → done
	err = graph.TransitionState(ctx, action, StateCompleted, nil)
	assert.NoError(t, err, "running → done 应成功")

	node, err = graph.GetNode(ctx, action)
	require.NoError(t, err)
	assert.Equal(t, StateCompleted, *node.State)

	t.Log("✅ 正常状态转换不受影响")
}

// TestTransitionState_AbortedToAborted 验证 aborted → aborted 允许（幂等）
func TestTransitionState_AbortedToAborted(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	taskID := "test-task"
	stateOpen := StateOpen
	action, err := graph.CreateNode(ctx, Node{
		TaskID:  taskID,
		Kind:    core.KindAction,
		State:   &stateOpen,
		Content: []byte(`{"instruction":"test"}`),
	})
	require.NoError(t, err)

	// 设置为 aborted
	reason1 := "first kill"
	err = graph.TransitionState(ctx, action, StateAborted, &reason1)
	require.NoError(t, err)

	// 再次设置为 aborted（应该允许，幂等操作）
	reason2 := "second kill (redundant)"
	err = graph.TransitionState(ctx, action, StateAborted, &reason2)
	assert.NoError(t, err, "aborted → aborted 应允许（幂等）")

	// 验证 reason 被更新
	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	assert.Equal(t, StateAborted, *node.State)
	assert.Equal(t, reason2, *node.BlockedReason, "reason 应更新为最新的")

	t.Log("✅ aborted → aborted 允许（幂等操作）")
}

// TestTransitionState_ConcurrentKill 验证并发 kill 场景
func TestTransitionState_ConcurrentKill(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	taskID := "test-task"
	stateRunning := StateRunning
	action, err := graph.CreateNode(ctx, Node{
		TaskID:  taskID,
		Kind:    core.KindAction,
		State:   &stateRunning,
		Content: []byte(`{"instruction":"test"}`),
	})
	require.NoError(t, err)

	// 模拟并发场景：
	// 1. Monitor kill (aborted)
	// 2. Executor 完成 (done)

	// Monitor 先 kill
	killReason := "monitor killed"
	err = graph.TransitionState(ctx, action, StateAborted, &killReason)
	require.NoError(t, err)

	// Executor 尝试标记 done（应该失败）
	err = graph.TransitionState(ctx, action, StateCompleted, nil)
	assert.Error(t, err, "Executor 的 done 更新应被拒绝")

	// 验证最终状态为 aborted（Monitor 的决策获胜）
	node, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	assert.Equal(t, StateAborted, *node.State)
	assert.Equal(t, killReason, *node.BlockedReason)

	t.Log("✅ 并发场景下 Monitor 的 kill 决策获胜")
}

// TestTransitionState_ErrorPropagation 验证错误传播
func TestTransitionState_ErrorPropagation(t *testing.T) {
	ctx := context.Background()
	graph := NewMemoryStore()

	// 尝试更新不存在的节点
	err := graph.TransitionState(ctx, "non-existent", StateCompleted, nil)
	assert.Error(t, err, "更新不存在的节点应返回错误")

	t.Log("✅ 错误正确传播")
}
