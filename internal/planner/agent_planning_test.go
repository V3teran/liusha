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
	converged := 0
	a := &Agent{taskID: revisionTask, graph: store, eventBus: &fakeEventBus{}, logger: zerolog.Nop(),
		onConverged: func(string) { converged++ }}
	ctx := context.Background()

	require.NoError(t, a.applyPlan(ctx, []explorationgraph.ActionDraft{
		planAction("p1", " 探测登录接口 "),
		planAction("p2", "  "),
	}, "planning"))

	actions, err := store.ListNodesByKind(ctx, revisionTask, core.KindAction)
	require.NoError(t, err)
	assert.Len(t, actions, 1, "全部提案都应被过滤")
	assert.Equal(t, 1, converged, "零新增动作且图上无未完结工作 → 收敛")
}

// 已中止的动作（任务收口或目标调整时被撤回）没有做完，也不含对错判断：规划者按当前
// 目标重新提出同一指令时不能被当作重复拦下；已否决的动作则要拦。
func TestApplyPlan_AbortedInstructionMayBeReproposed(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	aborted := explorationgraph.StateAborted
	rejected := explorationgraph.StateRejected
	withdrawn := revisionNode("a1", core.KindAction, explorationgraph.SourcePlanner, &aborted, time.Second)
	withdrawn.Content = []byte(`{"instruction":"探测登录接口"}`)
	vetoed := revisionNode("a2", core.KindAction, explorationgraph.SourcePlanner, &rejected, 2*time.Second)
	vetoed.Content = []byte(`{"instruction":"爆破后台口令"}`)
	for _, n := range []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		withdrawn, vetoed,
	} {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}
	a := &Agent{taskID: revisionTask, graph: store, eventBus: &fakeEventBus{}, logger: zerolog.Nop(),
		onConverged: func(string) {}}
	ctx := context.Background()

	require.NoError(t, a.applyPlan(ctx, []explorationgraph.ActionDraft{
		planAction("p1", "探测登录接口"),
		planAction("p2", "爆破后台口令"),
	}, "planning"))

	open, err := store.ListOpenActions(ctx, revisionTask)
	require.NoError(t, err)
	require.Len(t, open, 1, "只有被撤回的指令可以重新提出")
	assert.Equal(t, "p1", open[0].ID)
}
