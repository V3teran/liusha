package explorationgraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

func seedOpenAction(t *testing.T, store *Store, id, taskID string) {
	t.Helper()
	_, err := store.CreateNode(context.Background(), mkActionNode(id, taskID, StateOpen))
	require.NoError(t, err)
}

func claimedBy(t *testing.T, store *Store, id string) string {
	t.Helper()
	n, err := store.GetNode(context.Background(), id)
	require.NoError(t, err)
	return n.ClaimedBy()
}

// 认领记下 run 归属；同一动作只能被认领一次。
func TestClaimAction_RecordsOwnerOnce(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seedOpenAction(t, store, "a1", "t1")

	ok, err := store.ClaimAction(ctx, "t1", "a1", "run-1")
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "run-1", claimedBy(t, store, "a1"))

	ok, err = store.ClaimAction(ctx, "t1", "a1", "run-2")
	require.NoError(t, err)
	assert.False(t, ok, "running 的动作不能被再次认领")
}

// 交还：中断的动作回到 open，可被重新认领。
func TestReleaseAction_ReturnsToOpen(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seedOpenAction(t, store, "a1", "t1")
	_, err := store.ClaimAction(ctx, "t1", "a1", "run-1")
	require.NoError(t, err)

	ok, err := store.ReleaseAction(ctx, "t1", "a1", "run-1")
	require.NoError(t, err)
	assert.True(t, ok)
	n, err := store.GetNode(ctx, "a1")
	require.NoError(t, err)
	assert.Equal(t, StateOpen, *n.State)
}

// 归属校验：新 run 回收并重新认领后，旧 run 迟到的结束与交还都不得生效。
func TestFinishAction_RejectsStaleOwner(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seedOpenAction(t, store, "a1", "t1")
	_, err := store.ClaimAction(ctx, "t1", "a1", "run-old")
	require.NoError(t, err)

	reclaimed, err := store.ReclaimRunningActions(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, 1, reclaimed)
	ok, err := store.ClaimAction(ctx, "t1", "a1", "run-new")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = store.FinishAction(ctx, "t1", "a1", "run-old", StateCompleted, "")
	require.NoError(t, err)
	assert.False(t, ok, "旧 run 的结束不得覆盖新 run 的认领")
	ok, err = store.ReleaseAction(ctx, "t1", "a1", "run-old")
	require.NoError(t, err)
	assert.False(t, ok, "旧 run 不得交还新 run 认领的动作")

	n, err := store.GetNode(ctx, "a1")
	require.NoError(t, err)
	assert.Equal(t, StateRunning, *n.State)
	assert.Equal(t, "run-new", n.ClaimedBy())

	ok, err = store.FinishAction(ctx, "t1", "a1", "run-new", StateFailed, "工具超时")
	require.NoError(t, err)
	assert.True(t, ok)
	n, err = store.GetNode(ctx, "a1")
	require.NoError(t, err)
	assert.Equal(t, StateFailed, *n.State)
	require.NotNil(t, n.StateReason)
	assert.Equal(t, "工具超时", *n.StateReason)
}

// 外部改写（monitor 中止）后，执行方的结束不得覆盖外部决定。
func TestFinishAction_RespectsExternalAbort(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seedOpenAction(t, store, "a1", "t1")
	_, err := store.ClaimAction(ctx, "t1", "a1", "run-1")
	require.NoError(t, err)
	ok, err := store.RejectAction(ctx, "t1", "a1", SourceMonitor, "偏离目标")
	require.NoError(t, err)
	require.True(t, ok)

	ok, err = store.FinishAction(ctx, "t1", "a1", "run-1", StateCompleted, "")
	require.NoError(t, err)
	assert.False(t, ok)
}

// 否决：待执行与执行中的动作都可否决，记下否决方与原因；已结束的动作不受影响。
func TestRejectAction_OpenOrRunningOnly(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seedOpenAction(t, store, "open", "t1")
	seedOpenAction(t, store, "running", "t1")
	seedOpenAction(t, store, "done", "t1")
	_, err := store.ClaimAction(ctx, "t1", "running", "run-1")
	require.NoError(t, err)
	_, err = store.ClaimAction(ctx, "t1", "done", "run-1")
	require.NoError(t, err)
	_, err = store.FinishAction(ctx, "t1", "done", "run-1", StateCompleted, "")
	require.NoError(t, err)

	for _, id := range []string{"open", "running"} {
		ok, err := store.RejectAction(ctx, "t1", id, SourceUser, "不在授权范围")
		require.NoError(t, err)
		assert.True(t, ok, id)
		n, err := store.GetNode(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, StateRejected, *n.State)
		assert.Equal(t, SourceUser, n.RejectedBy())
		require.NotNil(t, n.StateReason)
		assert.Equal(t, "不在授权范围", *n.StateReason)
	}

	ok, err := store.RejectAction(ctx, "t1", "done", SourceUser, "迟到")
	require.NoError(t, err)
	assert.False(t, ok, "已结束的动作不能否决")
}

// 否决只作用于本任务的动作节点：别的任务的节点、非动作节点一律拒绝。
func TestRejectAction_ScopedToTaskActions(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seedOpenAction(t, store, "a1", "t1")
	open := StateOpen
	_, err := store.CreateNode(ctx, Node{ID: "o1", TaskID: "t1", Kind: core.KindObjective, State: &open,
		Content: []byte(`{"description":"g"}`), SourceType: SourceUser})
	require.NoError(t, err)

	_, err = store.RejectAction(ctx, "t2", "a1", SourceUser, "r")
	assert.ErrorIs(t, err, ErrActionNotFound)
	_, err = store.RejectAction(ctx, "t1", "o1", SourceUser, "r")
	assert.ErrorIs(t, err, ErrActionNotFound)
}

// 回收只作用于本任务的 running 动作。
func TestReclaimRunningActions_ScopedToTask(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seedOpenAction(t, store, "a1", "t1")
	seedOpenAction(t, store, "b1", "t2")
	_, err := store.ClaimAction(ctx, "t1", "a1", "run-1")
	require.NoError(t, err)
	_, err = store.ClaimAction(ctx, "t2", "b1", "run-2")
	require.NoError(t, err)

	n, err := store.ReclaimRunningActions(ctx, "t1")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	other, err := store.GetNode(ctx, "b1")
	require.NoError(t, err)
	assert.Equal(t, StateRunning, *other.State, "别的任务的在途动作不受影响")
	assert.Equal(t, core.KindAction, other.Kind)
}
