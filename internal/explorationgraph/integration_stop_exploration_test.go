//go:build integration

package explorationgraph

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// TestStopExplorationPostgres 验证停止探索在 PG 后端的真实落库：版本前置、对规划者
// 待执行与执行中动作的收回、用户指派动作不动、原因随状态写入且不抹掉身份字段。
func TestStopExplorationPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}

	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()

	store := NewStore(pool)
	const (
		taskID    = "ccccccc1-cccc-4ccc-8ccc-cccccccccccc"
		rootID    = "2c3f6a9e-2222-4222-8222-000000000001"
		openID    = "2c3f6a9e-2222-4222-8222-000000000002"
		runningID = "2c3f6a9e-2222-4222-8222-000000000003"
		doneID    = "2c3f6a9e-2222-4222-8222-000000000004"
		userID    = "2c3f6a9e-2222-4222-8222-000000000005"
	)
	mk := func(id string, kind core.NodeKind, source SourceType, state State) Node {
		return Node{ID: id, TaskID: taskID, Kind: kind, State: &state, SourceType: source,
			Content: []byte(`{"description":"d","instruction":"i"}`), CreatedAt: time.Now()}
	}
	for _, n := range []Node{
		mk(rootID, core.KindObjective, SourceUser, StateOpen),
		mk(openID, core.KindAction, SourcePlanner, StateOpen),
		mk(runningID, core.KindAction, SourcePlanner, StateRunning),
		mk(doneID, core.KindAction, SourcePlanner, StateCompleted),
		mk(userID, core.KindAction, SourceUser, StateRunning),
	} {
		_, err := store.CreateNode(ctx, n)
		require.NoError(t, err)
	}
	stateOfNode := func(id string) State {
		n, err := store.GetNode(ctx, id)
		require.NoError(t, err)
		return *n.State
	}

	basis, err := store.GoalBasis(ctx, taskID)
	require.NoError(t, err)

	// 依据已失效：什么都不收回
	require.NoError(t, store.UpdateNodeContent(ctx, rootID, MustJSON(map[string]string{"description": "新目标"})))
	_, err = store.StopExploration(ctx, taskID, basis, "停止")
	require.ErrorIs(t, err, ErrGoalChanged)
	assert.Equal(t, StateRunning, stateOfNode(runningID))

	basis, err = store.GoalBasis(ctx, taskID)
	require.NoError(t, err)
	withdrawn, err := store.StopExploration(ctx, taskID, basis, "停止")
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{openID, runningID}, withdrawn)
	assert.Equal(t, StateAborted, stateOfNode(openID))
	assert.Equal(t, StateAborted, stateOfNode(runningID))
	assert.Equal(t, StateCompleted, stateOfNode(doneID))
	assert.Equal(t, StateRunning, stateOfNode(userID))

	n, err := store.GetNode(ctx, runningID)
	require.NoError(t, err)
	require.NotNil(t, n.StateReason)
	assert.Equal(t, "停止", *n.StateReason)
	assert.Equal(t, SourcePlanner, n.SourceType)
}
