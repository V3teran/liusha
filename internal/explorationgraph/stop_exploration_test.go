package explorationgraph

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

func actionNode(id string, source SourceType, state State) Node {
	return Node{ID: id, Kind: core.KindAction, State: stateOf(state),
		SourceType: source, Content: []byte(`{"instruction":"` + id + `"}`)}
}

// stopFixture: 根目标 root；规划者的 p-open（待执行）、p-running（执行中）、p-done（已完成）；
// 用户指派的 u-running（执行中）。
func stopFixture(t *testing.T) *Store {
	t.Helper()
	store := NewMemoryStore()
	seedNode(t, store, rootObjective("root"))
	seedNode(t, store, actionNode("p-open", SourcePlanner, StateOpen))
	seedNode(t, store, actionNode("p-running", SourcePlanner, StateRunning))
	seedNode(t, store, actionNode("p-done", SourcePlanner, StateCompleted))
	seedNode(t, store, actionNode("u-running", SourceUser, StateRunning))
	return store
}

// 规划者停止探索：自己排出、还没做完的动作（待执行与执行中）一并收回为已中止并记下原因；
// 已结束的动作与用户指派的动作不动。
func TestStopExploration_WithdrawsPlannerUnfinishedActions(t *testing.T) {
	store := stopFixture(t)
	ctx := context.Background()

	withdrawn, err := store.StopExploration(ctx, revTask, basisOf(t, store), "目标已达成")
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"p-open", "p-running"}, withdrawn)
	assert.Equal(t, StateAborted, nodeState(t, store, "p-open"))
	assert.Equal(t, StateAborted, nodeState(t, store, "p-running"))
	assert.Equal(t, StateCompleted, nodeState(t, store, "p-done"))
	assert.Equal(t, StateRunning, nodeState(t, store, "u-running"), "用户指派的动作照常做完")

	n, err := store.GetNode(ctx, "p-running")
	require.NoError(t, err)
	require.NotNil(t, n.StateReason)
	assert.Equal(t, "目标已达成", *n.StateReason)
	assert.Equal(t, SourcePlanner, n.SourceType, "批注不得抹掉身份字段")
}

// 停止的结论针对它所依据的目标：规划期间目标被改写，什么都不收回。
func TestStopExploration_DiscardedWhenGoalChangedSinceBasis(t *testing.T) {
	store := stopFixture(t)
	basis := basisOf(t, store)

	rewriteGoal(t, store, "新目标")
	_, err := store.StopExploration(context.Background(), revTask, basis, "目标已达成")

	require.ErrorIs(t, err, ErrGoalChanged)
	assert.Equal(t, StateOpen, nodeState(t, store, "p-open"))
	assert.Equal(t, StateRunning, nodeState(t, store, "p-running"))
}

// 没有可收回的动作时停止依然成立（不报错），但仍以依据为前置。
func TestStopExploration_NothingToWithdraw(t *testing.T) {
	store := NewMemoryStore()
	seedNode(t, store, rootObjective("root"))
	basis := basisOf(t, store)

	withdrawn, err := store.StopExploration(context.Background(), revTask, basis, "无事可做")
	require.NoError(t, err)
	assert.Empty(t, withdrawn)

	rewriteGoal(t, store, "新目标")
	_, err = store.StopExploration(context.Background(), revTask, basis, "无事可做")
	require.ErrorIs(t, err, ErrGoalChanged)
}
