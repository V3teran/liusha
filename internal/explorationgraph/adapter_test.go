package explorationgraph

import (
	"context"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkActionNode 构造一个 action 节点（生产路径经 CreateNode 直写）。
func mkActionNode(id, taskID string, state State, deps []string) Node {
	st := state
	return Node{
		ID:         id,
		TaskID:     taskID,
		Kind:       core.KindAction,
		Content:    []byte(`{"instruction":"scan"}`),
		State:      &st,
		DependsOn:  deps,
		SourceType: SourcePlanner,
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
}

// TestAdapterStore_CreateNode 验证生产主路径 CreateNode：action 落地带状态。
func TestAdapterStore_CreateNode(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	objID := "obj-1"
	_, err := store.CreateNode(ctx, Node{
		ID:      objID,
		TaskID:  "task-001",
		Kind:    core.KindObjective,
		Content: []byte(`{"description":"scan target"}`),
	})
	require.NoError(t, err)

	_, err = store.CreateNode(ctx, mkActionNode("action-1", "task-001", StateOpen, nil))
	require.NoError(t, err)

	node, err := store.GetNode(ctx, "action-1")
	require.NoError(t, err)
	assert.Equal(t, core.KindAction, node.Kind)
	assert.Equal(t, core.ActionStateOpen, *node.State)
}

// TestAdapterStore_ActionStateManagement 验证状态机：列表、带理由状态迁移、CAS。
func TestAdapterStore_ActionStateManagement(t *testing.T) {
	store := NewMemoryStore()
	ctx := context.Background()

	_, err := store.CreateNode(ctx, mkActionNode("action-open-1", "task-001", StateOpen, nil))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkActionNode("action-open-2", "task-001", StateOpen, nil))
	require.NoError(t, err)
	_, err = store.CreateNode(ctx, mkActionNode("action-done", "task-001", StateDone, nil))
	require.NoError(t, err)

	t.Run("ListOpenActions 只返回 open", func(t *testing.T) {
		openActions, err := store.ListOpenActions(ctx, "task-001")
		require.NoError(t, err)
		assert.Len(t, openActions, 2)
	})

	t.Run("UpdateActionStateWithReason 迁移并留痕", func(t *testing.T) {
		blocked := "missing credentials"
		require.NoError(t, store.UpdateActionStateWithReason(ctx, "action-open-1", StateBlocked, &blocked))

		node, err := store.GetNode(ctx, "action-open-1")
		require.NoError(t, err)
		assert.Equal(t, core.ActionStateBlocked, *node.State)
	})

	t.Run("CompareAndSwapActionState 并发抢占", func(t *testing.T) {
		ok, err := store.CompareAndSwapActionState(ctx, "task-001", "action-open-2", StateOpen, StateRunning, nil)
		require.NoError(t, err)
		assert.True(t, ok, "状态匹配应抢占成功")

		ok, err = store.CompareAndSwapActionState(ctx, "task-001", "action-open-2", StateOpen, StateRunning, nil)
		require.NoError(t, err)
		assert.False(t, ok, "状态不匹配 CAS 应失败")
	})
}

// TestAdapterStore_RecordVerification_MemoryStoreRejects 验证内存 store 无 pool 时审计链落档明确报错。
func TestAdapterStore_RecordVerification_MemoryStoreRejects(t *testing.T) {
	store := NewMemoryStore()

	_, err := store.RecordVerification(context.Background(), Verification{
		ID: "v-1", TaskID: "task-001", NodeID: "n-1",
		Outcome: OutcomeConfirmed,
	})
	require.Error(t, err, "内存 store 无 pool，RecordVerification 应明确报错")
}
