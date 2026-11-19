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
		onConverged: func(string, explorationgraph.GoalBasis) { converged++ }}
	ctx := context.Background()

	require.NoError(t, a.applyOutcome(ctx, basisOf(t, store), graphView{}, PlanOutcome{Actions: []explorationgraph.ActionDraft{
		planAction("p1", " 探测登录接口 "),
		planAction("p2", "  "),
	}}, "planning"))

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
		onConverged: func(string, explorationgraph.GoalBasis) {}}
	ctx := context.Background()

	require.NoError(t, a.applyOutcome(ctx, basisOf(t, store), graphView{}, PlanOutcome{Actions: []explorationgraph.ActionDraft{
		planAction("p1", "探测登录接口"),
		planAction("p2", "爆破后台口令"),
	}}, "planning"))

	open, err := store.ListOpenActions(ctx, revisionTask)
	require.NoError(t, err)
	require.Len(t, open, 1, "只有被撤回的指令可以重新提出")
	assert.Equal(t, "p1", open[0].ID)
}

// goalShiftingPlanner 在第一次规划（或分析）期间改写任务目标，模拟用户在 LLM 调用
// 期间调整目标；之后各次按 plans 依次给出提案。
type goalShiftingPlanner struct {
	store          *explorationgraph.Store
	plans          [][]explorationgraph.ActionDraft
	analysis       *ResultAnalysis
	shiftOnAnalyze bool
	planCalls      int
	analyzeCalls   int
}

func (p *goalShiftingPlanner) Plan(ctx context.Context, _ *explorationgraph.Store, taskID string, _ PlanRequest) (PlanOutcome, error) {
	p.planCalls++
	if p.planCalls == 1 && !p.shiftOnAnalyze {
		if err := p.shiftGoal(ctx, taskID); err != nil {
			return PlanOutcome{}, err
		}
	}
	if p.planCalls > len(p.plans) {
		return PlanOutcome{}, nil
	}
	return PlanOutcome{Actions: p.plans[p.planCalls-1]}, nil
}

func (p *goalShiftingPlanner) AnalyzeResults(ctx context.Context, _ *explorationgraph.Store, taskID string, _ []explorationgraph.Node) (*ResultAnalysis, error) {
	p.analyzeCalls++
	if p.analyzeCalls == 1 && p.shiftOnAnalyze {
		if err := p.shiftGoal(ctx, taskID); err != nil {
			return nil, err
		}
	}
	return p.analysis, nil
}

// shiftGoal 改写根目标的描述（生产路径经 task.Store.SetGoal 与任务行同事务写入）。
func (p *goalShiftingPlanner) shiftGoal(ctx context.Context, taskID string) error {
	root, _, err := p.store.RootObjectiveOf(ctx, taskID)
	if err != nil {
		return err
	}
	return p.store.UpdateNodeContent(ctx, root.ID, explorationgraph.MustJSON(map[string]string{"description": "调整后的目标"}))
}

func rootOnlyStore(t *testing.T) *explorationgraph.Store {
	t.Helper()
	store := explorationgraph.NewMemoryStore()
	_, err := store.CreateNode(context.Background(), revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0))
	require.NoError(t, err)
	return store
}

// 规划期间目标被改写：按旧目标得出的整批提案作废，立即按新目标重新规划。
func TestPlanActions_GoalChangedDuringPlanningDiscardsTheBatch(t *testing.T) {
	store := rootOnlyStore(t)
	planner := &goalShiftingPlanner{store: store, plans: [][]explorationgraph.ActionDraft{
		{planAction("p-old", "按旧目标探测")},
		{planAction("p-new", "按新目标探测")},
	}}
	a := &Agent{taskID: revisionTask, graph: store, planner: planner, eventBus: &fakeEventBus{}, logger: zerolog.Nop(),
		onConverged: func(string, explorationgraph.GoalBasis) { t.Fatal("有新动作时不应收敛") }}
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))

	assert.Equal(t, 2, planner.planCalls, "目标变化后立即重新规划")
	open, err := store.ListOpenActions(ctx, revisionTask)
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, "p-new", open[0].ID, "按旧目标的提案整批不落")
}

// 规划期间目标被改写、而旧目标下的规划给不出新方向：不能据此收敛（会把新目标直接
// 结束），而是按新目标重新规划；收敛携带它所依据的目标。
func TestPlanActions_GoalChangedBeforeConvergenceReplans(t *testing.T) {
	store := rootOnlyStore(t)
	planner := &goalShiftingPlanner{store: store}
	var converged []explorationgraph.GoalBasis
	a := &Agent{taskID: revisionTask, graph: store, planner: planner, eventBus: &fakeEventBus{}, logger: zerolog.Nop(),
		onConverged: func(_ string, basis explorationgraph.GoalBasis) { converged = append(converged, basis) }}
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))

	assert.Equal(t, 2, planner.planCalls)
	current, err := store.GoalBasis(ctx, revisionTask)
	require.NoError(t, err)
	assert.Equal(t, []explorationgraph.GoalBasis{current}, converged, "只按新目标的规划宣告收敛")
}

// 结果分析期间目标被改写：分析结论连同水位作废，按新目标重新分析。
func TestPlanActions_GoalChangedDuringAnalysisReanalyzes(t *testing.T) {
	a, store := revisionAgent(t, nil)
	planner := &goalShiftingPlanner{store: store, shiftOnAnalyze: true,
		analysis: &ResultAnalysis{ContinuationActions: []ContinuationAction{{Instruction: "沿结果深挖"}}}}
	a.planner = planner
	a.onConverged = func(string, explorationgraph.GoalBasis) {}
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))

	assert.Equal(t, 2, planner.analyzeCalls, "目标变化后立即重新分析")
	assert.True(t, isAnalyzed(t, store, "r1"))
	actions, err := store.ListNodesByKind(ctx, revisionTask, core.KindAction)
	require.NoError(t, err)
	assert.Len(t, actions, 1, "作废的那次分析什么都没落")
}

// basisOf 读取任务当前的目标依据。
func basisOf(t *testing.T, store *explorationgraph.Store) explorationgraph.GoalBasis {
	t.Helper()
	basis, err := store.GoalBasis(context.Background(), revisionTask)
	require.NoError(t, err)
	return basis
}
