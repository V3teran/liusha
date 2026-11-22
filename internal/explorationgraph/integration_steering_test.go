//go:build integration

package explorationgraph

import (
	"context"
	"github.com/V3teran/liusha/internal/dbtest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// 运行中引导在调用方事务内写图：撤回未开始的规划动作、指派动作、否决动作。
func TestSteeringPostgres(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过集成测试")
	}
	ctx := context.Background()
	pool := setupTestDB(t)
	defer pool.Close()
	store := NewStore(pool)

	const (
		taskID  = "ccccccc5-cccc-4ccc-8ccc-cccccccccccc"
		other   = "ccccccc6-cccc-4ccc-8ccc-cccccccccccc"
		planned = "5c2f6a9e-1111-4111-8111-000000000001"
		running = "5c2f6a9e-1111-4111-8111-000000000002"
		mine    = "5c2f6a9e-1111-4111-8111-000000000003"
		done    = "5c2f6a9e-1111-4111-8111-000000000004"
	)
	dbtest.SeedTask(t, pool, taskID)
	dbtest.SeedTask(t, pool, other)
	require.NoError(t, CreateRootObjective(ctx, pool, taskID, "目标"))
	action := func(id string, state State, source SourceType) {
		_, err := store.CreateNode(ctx, Node{ID: id, TaskID: taskID, Kind: core.KindAction, State: &state,
			SourceType: source, Priority: PriorityMedium, Content: []byte(`{"instruction":"x"}`), CreatedAt: time.Now()})
		require.NoError(t, err)
	}
	action(planned, StateOpen, SourcePlanner)
	action(running, StateRunning, SourcePlanner)
	action(mine, StateOpen, SourceUser)
	action(done, StateCompleted, SourcePlanner)

	t.Run("撤回只动未开始的规划动作", func(t *testing.T) {
		n, err := WithdrawPlannedActions(ctx, pool, taskID, "目标已调整，撤回未执行的规划")
		require.NoError(t, err)
		assert.Equal(t, int64(1), n)
		node, err := store.GetNode(ctx, planned)
		require.NoError(t, err)
		assert.Equal(t, StateAborted, *node.State)
		assert.Equal(t, "目标已调整，撤回未执行的规划", *node.StateReason)
		assert.Equal(t, SourcePlanner, node.SourceType, "只合并原因，不丢身份字段")
		for id, want := range map[string]State{running: StateRunning, mine: StateOpen, done: StateCompleted} {
			n, err := store.GetNode(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, want, *n.State, id)
		}
	})

	t.Run("指派动作挂在根目标下", func(t *testing.T) {
		id, err := AssignAction(ctx, pool, taskID, "复测登录接口的 SQL 注入", PriorityHigh)
		require.NoError(t, err)
		node, err := store.GetNode(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, StateOpen, *node.State)
		assert.Equal(t, SourceUser, node.SourceType)
		assert.Equal(t, PriorityHigh, node.Priority)
		assert.JSONEq(t, `{"instruction":"复测登录接口的 SQL 注入"}`, string(node.Content))
		root, _, err := store.RootObjectiveOf(ctx, taskID)
		require.NoError(t, err)
		edges, err := store.ListEdgesByTask(ctx, taskID)
		require.NoError(t, err)
		found := false
		for _, e := range edges {
			found = found || (e.SrcID == root.ID && e.Rel == RelGenerates && e.DstID == id)
		}
		assert.True(t, found, "根目标 → 指派动作的 generates 边")

		_, err = AssignAction(ctx, pool, other, "x", PriorityHigh)
		assert.Error(t, err, "没有根目标的任务无处挂动作")
	})

	t.Run("否决待执行与执行中的动作", func(t *testing.T) {
		require.NoError(t, RejectActionTx(ctx, pool, taskID, running, SourceUser, "范围外"))
		node, err := store.GetNode(ctx, running)
		require.NoError(t, err)
		assert.Equal(t, StateRejected, *node.State)
		assert.Equal(t, SourceUser, node.RejectedBy())
		assert.Equal(t, "范围外", *node.StateReason)

		assert.ErrorIs(t, RejectActionTx(ctx, pool, taskID, done, SourceUser, "x"), ErrActionSettled)
		assert.ErrorIs(t, RejectActionTx(ctx, pool, taskID, running, SourceUser, "x"), ErrActionSettled, "已否决即已结束")
		assert.ErrorIs(t, RejectActionTx(ctx, pool, other, mine, SourceUser, "x"), ErrActionNotFound, "别的任务的动作")
		assert.ErrorIs(t, RejectActionTx(ctx, pool, taskID, "5c2f6a9e-1111-4111-8111-0000000000ff", SourceUser, "x"), ErrActionNotFound)
	})
}
