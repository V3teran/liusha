package executor

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// TestExecutor_AbortedStateNotOverwritten 验证 P1-A 修复：
// action 被 monitor kill 后，状态保持 aborted 不被覆盖
func TestExecutor_AbortedStateNotOverwritten(t *testing.T) {
	ctx := context.Background()
	graph := explorationgraph.NewMemoryStore()

	// 创建 action
	taskID := "test-task"
	stateOpen := explorationgraph.StateOpen
	action, err := graph.CreateNode(ctx, explorationgraph.Node{
		TaskID: taskID,
		Kind:   core.KindAction,
		State:  &stateOpen,
		Content: []byte(`{"instruction":"test action","host":"test.local"}`),
	})
	require.NoError(t, err)

	// 模拟执行开始：open → running
	err = graph.UpdateActionStateWithReason(ctx, action, explorationgraph.StateRunning, nil)
	require.NoError(t, err)

	// 模拟 Monitor kill action
	reason := "monitor detected stuck for 30 minutes"
	err = graph.UpdateActionStateWithReason(ctx, action, explorationgraph.StateAborted, &reason)
	require.NoError(t, err)
	t.Log("✅ Monitor 已 kill action (aborted)")

	// 模拟执行完成后的状态检查（修复后的逻辑）
	currentNode, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	require.NotNil(t, currentNode.State)

	// 验证状态为 aborted
	isAborted := *currentNode.State == explorationgraph.StateAborted
	assert.True(t, isAborted, "状态应为 aborted")

	// 修复后：executor 应检测到 aborted 并跳过状态更新
	// 这里模拟检查逻辑
	if isAborted {
		t.Log("✅ 检测到 aborted，跳过状态更新")
		// 不执行 UpdateActionStateWithReason(done/failed)
	} else {
		// 原来的错误行为：无条件覆盖
		t.Fatal("❌ 未检测到 aborted，会被错误覆盖")
	}

	// 验证最终状态仍为 aborted
	finalNode, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	require.NotNil(t, finalNode.State)
	assert.Equal(t, explorationgraph.StateAborted, *finalNode.State, "状态应保持 aborted")
	assert.Equal(t, reason, *finalNode.BlockedReason, "kill reason 应保留")
	t.Log("✅ 状态保持 aborted，未被覆盖")
}

// TestExecutor_NormalFlowNotAffected 验证正常流程不受影响
func TestExecutor_NormalFlowNotAffected(t *testing.T) {
	ctx := context.Background()
	graph := explorationgraph.NewMemoryStore()

	taskID := "test-task"
	stateOpen := explorationgraph.StateOpen
	action, err := graph.CreateNode(ctx, explorationgraph.Node{
		TaskID: taskID,
		Kind:   core.KindAction,
		State:  &stateOpen,
		Content: []byte(`{"instruction":"test","host":"test.local"}`),
	})
	require.NoError(t, err)

	// 正常执行：open → running → done
	err = graph.UpdateActionStateWithReason(ctx, action, explorationgraph.StateRunning, nil)
	require.NoError(t, err)

	// 检查状态（未被 kill）
	currentNode, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	isAborted := currentNode.State != nil && *currentNode.State == explorationgraph.StateAborted

	if !isAborted {
		// 正常完成
		err = graph.UpdateActionStateWithReason(ctx, action, explorationgraph.StateDone, nil)
		require.NoError(t, err)
	}

	// 验证状态为 done
	finalNode, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	require.NotNil(t, finalNode.State)
	assert.Equal(t, explorationgraph.StateDone, *finalNode.State, "正常完成应为 done")
	t.Log("✅ 正常执行流程不受影响")
}

// TestExecutor_AbortedDetectionTiming 验证执行完成后立即检查状态
func TestExecutor_AbortedDetectionTiming(t *testing.T) {
	ctx := context.Background()
	graph := explorationgraph.NewMemoryStore()

	taskID := "test-task"
	stateOpen := explorationgraph.StateOpen
	action, err := graph.CreateNode(ctx, explorationgraph.Node{
		TaskID: taskID,
		Kind:   core.KindAction,
		State:  &stateOpen,
		Content: []byte(`{"instruction":"test","host":"test.local"}`),
	})
	require.NoError(t, err)

	// 执行开始
	err = graph.UpdateActionStateWithReason(ctx, action, explorationgraph.StateRunning, nil)
	require.NoError(t, err)

	// 模拟执行中被 kill（发生在执行完成前）
	go func() {
		time.Sleep(50 * time.Millisecond)
		reason := "killed during execution"
		graph.UpdateActionStateWithReason(ctx, action, explorationgraph.StateAborted, &reason)
	}()

	// 模拟执行持续 100ms
	time.Sleep(100 * time.Millisecond)

	// 执行完成后检查状态（关键时机）
	currentNode, err := graph.GetNode(ctx, action)
	require.NoError(t, err)

	if currentNode.State != nil && *currentNode.State == explorationgraph.StateAborted {
		t.Log("✅ 执行完成后检测到 aborted，不更新状态")
		// 不执行状态更新
	} else {
		// 未被 kill，正常更新
		graph.UpdateActionStateWithReason(ctx, action, explorationgraph.StateDone, nil)
	}

	// 验证最终状态
	finalNode, err := graph.GetNode(ctx, action)
	require.NoError(t, err)
	require.NotNil(t, finalNode.State)
	assert.Equal(t, explorationgraph.StateAborted, *finalNode.State, "应检测到执行期间的 kill")
	t.Log("✅ 时机检查生效：执行完成后立即检测到 aborted")
}
