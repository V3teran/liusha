package planner

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// 提案全被过滤（空指令 / 与既有动作重复）时图上没有新增动作：这与「没有提案」是同一
// 局面，必须走无动作处理（收敛判定），否则图静止而 planner 每次唤醒都再付费规划一遍。
func TestApplyPlan_AllProposalsFilteredIsNoActions(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	done := explorationgraph.StateCompleted
	existing := revisionNode("a1", core.KindAction, explorationgraph.SourcePlanner, &done, time.Second)
	existing.Content = []byte(`{"instruction":"探测登录接口"}`)
	for _, n := range []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		existing,
	} {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}
	a := &Agent{taskID: revisionTask, graph: store, eventBus: &fakeEventBus{}, logger: zerolog.Nop()}
	ctx := context.Background()

	require.NoError(t, a.applyPlan(ctx, []explorationgraph.Node{
		planAction("p1", " 探测登录接口 "),
		planAction("p2", "  "),
	}, "planning"))

	actions, err := store.ListNodesByKind(ctx, revisionTask, core.KindAction)
	require.NoError(t, err)
	assert.Len(t, actions, 1, "全部提案都应被过滤")
	assert.True(t, a.convergedPending, "零新增动作且图上无未完结工作 → 收敛并挂起后续重复规划")
}

// 有实际新增动作才解除收敛挂起。
func TestApplyPlan_WrittenActionsClearConvergence(t *testing.T) {
	a, _ := revisionAgent(t, &ResultAnalysis{})
	a.convergedPending = true

	require.NoError(t, a.applyPlan(context.Background(), []explorationgraph.Node{planAction("p1", "探测登录接口")}, "planning"))
	assert.False(t, a.convergedPending)
}
