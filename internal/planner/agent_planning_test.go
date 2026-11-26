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

	require.NoError(t, a.applyOutcome(ctx, basisOf(t, store), explorationgraph.Surface{}, PlanOutcome{Actions: []explorationgraph.ActionDraft{
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

	require.NoError(t, a.applyOutcome(ctx, basisOf(t, store), explorationgraph.Surface{}, PlanOutcome{Actions: []explorationgraph.ActionDraft{
		planAction("p1", "探测登录接口"),
		planAction("p2", "爆破后台口令"),
	}}, "planning"))

	open, err := store.ListOpenActions(ctx, revisionTask)
	require.NoError(t, err)
	require.Len(t, open, 1, "只有被撤回的指令可以重新提出")
	assert.Equal(t, "p1", open[0].ID)
}

// leadGraph 建一个带根目标与两条待深挖线索（lead-1、lead-2）的图。
func leadGraph(t *testing.T) (*explorationgraph.Store, context.Context) {
	store := explorationgraph.NewMemoryStore()
	for _, n := range []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		planFactLead("lead-1", 1), planFactLead("lead-2", 2),
	} {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}
	return store, context.Background()
}

// planFactLead 建一条被标为线索的事实观察。
func planFactLead(id string, offset time.Duration) explorationgraph.Node {
	n := contextObservation(id, "access", "探通了一条路 "+id, nil, "", offset)
	n.Metadata = explorationgraph.OpensDeeperMetadata()
	return n
}

func openLeadIDs(t *testing.T, store *explorationgraph.Store, ctx context.Context) []string {
	leads, err := store.OpenLeads(ctx, revisionTask)
	require.NoError(t, err)
	ids := make([]string, 0, len(leads))
	for _, lead := range leads {
		ids = append(ids, lead.ID)
	}
	return ids
}

// 认领线索的动作落图后带一条 pursues 边，被认领的线索退出待深挖清单，其余的仍在。
func TestApplyOutcome_PursuingActionClaimsLead(t *testing.T) {
	store, ctx := leadGraph(t)
	a := &Agent{taskID: revisionTask, graph: store, eventBus: &fakeEventBus{}, logger: zerolog.Nop(),
		onConverged: func(string, explorationgraph.GoalBasis) {}}
	draft := planAction("p1", "沿线索一深挖")
	draft.PursuesLead = "lead-1"

	require.NoError(t, a.applyOutcome(ctx, basisOf(t, store), explorationgraph.Surface{}, PlanOutcome{
		Actions: []explorationgraph.ActionDraft{draft},
	}, "planning"))

	pursued, err := store.PursuedLeads(ctx, revisionTask, "p1")
	require.NoError(t, err)
	require.Len(t, pursued, 1)
	assert.Equal(t, "lead-1", pursued[0].ID, "认领落成 pursues 边")
	assert.Equal(t, []string{"lead-2"}, openLeadIDs(t, store, ctx), "被认领的线索退出待深挖清单")
	assert.Nil(t, a.conclusion, "排了新动作，此前的结论作废")
}

// 只判定不追、不排新动作的一轮也是图面的变化：「等待」结论作废，不走收敛判定——否则
// 剩下的待深挖线索没人处理，任务会在这里收敛。
func TestApplyOutcome_DeclinedLeadsIsAGraphChange(t *testing.T) {
	store, ctx := leadGraph(t)
	converged := 0
	a := &Agent{taskID: revisionTask, graph: store, eventBus: &fakeEventBus{}, logger: zerolog.Nop(),
		onConverged: func(string, explorationgraph.GoalBasis) { converged++ }}

	require.NoError(t, a.applyOutcome(ctx, basisOf(t, store), explorationgraph.Surface{}, PlanOutcome{
		DeclinedLeads: []explorationgraph.LeadDecline{{LeadID: "lead-1", Reason: "超出任务目标"}},
	}, "planning"))

	lead, err := store.GetNode(ctx, "lead-1")
	require.NoError(t, err)
	assert.Equal(t, "超出任务目标", lead.LeadDeclineReason(), "不追的理由落在线索上")
	assert.Equal(t, []string{"lead-2"}, openLeadIDs(t, store, ctx))
	assert.Nil(t, a.conclusion, "图面有变化，等待结论不作数")
	assert.Equal(t, 0, converged, "不因这一轮没有新动作就走收敛判定")
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
		analysis: &ResultAnalysis{NewObjectives: []NewObjective{{Description: "沿结果深挖"}}}}
	a.planner = planner
	a.onConverged = func(string, explorationgraph.GoalBasis) {}
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))

	assert.Equal(t, 2, planner.analyzeCalls, "目标变化后立即重新分析")
	assert.True(t, isAnalyzed(t, store, "r1"))
	objectives, err := store.ListNodesByKind(ctx, revisionTask, core.KindObjective)
	require.NoError(t, err)
	assert.Len(t, objectives, 4, "作废的那次分析什么都没落：只有重做的那次落下一个新方向")
}

// basisOf 读取任务当前的目标依据。
func basisOf(t *testing.T, store *explorationgraph.Store) explorationgraph.GoalBasis {
	t.Helper()
	basis, err := store.GoalBasis(context.Background(), revisionTask)
	require.NoError(t, err)
	return basis
}

// directionPlanner 记录常规规划被调用时图上进行中的派生目标：验证结果分析修订的方向
// 在同一轮里就交给看得到全貌的常规规划。
type directionPlanner struct {
	store       *explorationgraph.Store
	analysis    *ResultAnalysis
	plan        []explorationgraph.ActionDraft
	planCalls   int
	liveAtPlans []string
}

func (p *directionPlanner) Plan(ctx context.Context, _ *explorationgraph.Store, taskID string, _ PlanRequest) (PlanOutcome, error) {
	p.planCalls++
	objectives, err := p.store.ListNodesByKind(ctx, taskID, core.KindObjective)
	if err != nil {
		return PlanOutcome{}, err
	}
	for _, o := range objectives {
		if o.IsDerivedObjective() && o.State != nil && *o.State == explorationgraph.StateOpen {
			p.liveAtPlans = append(p.liveAtPlans, o.ID)
		}
	}
	return PlanOutcome{Actions: p.plan}, nil
}

func (p *directionPlanner) AnalyzeResults(context.Context, *explorationgraph.Store, string, []explorationgraph.Node) (*ResultAnalysis, error) {
	return p.analysis, nil
}

// 结果分析只修订方向，动作由同一轮的常规规划排出：常规规划看得到分析刚落下的新方向，
// 不会因为分析先排了动作而被跳过（实测：分析看不到已有事实与动作，排出的重复动作挤掉了
// 看得到全貌的那次规划）。
func TestPlanActions_AnalysisRevisesDirectionThenPlanningSchedules(t *testing.T) {
	a, store := revisionAgent(t, nil)
	planner := &directionPlanner{
		store:    store,
		analysis: &ResultAnalysis{CompletedObjectives: []explorationgraph.ObjectiveCompletion{{ObjectiveID: "d1", Evidence: []string{"r1"}}}},
		plan:     []explorationgraph.ActionDraft{planAction("p1", "沿新结果探测")},
	}
	a.planner = planner
	a.onConverged = func(string, explorationgraph.GoalBasis) { t.Fatal("有新动作时不应收敛") }
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))

	assert.True(t, isAnalyzed(t, store, "r1"))
	assert.Equal(t, 1, planner.planCalls, "分析之后同一轮即常规规划")
	assert.Equal(t, []string{"d2"}, planner.liveAtPlans, "常规规划看到的是分析修订后的方向")
	open, err := store.ListOpenActions(ctx, revisionTask)
	require.NoError(t, err)
	require.Len(t, open, 1)
	assert.Equal(t, "p1", open[0].ID)
}

// 带着所见图面的重规划请求，在处理时图面已有实质进展（目标改写、新的派生目标、新结果）
// 即作废：判断的前提已经变了（实测：监察者基于「1/2 已坐实」的图面请求重规划，三分钟后
// 才被处理，那时两个结果都已坐实）。新增的动作不使请求作废——原地打转时动作照样增长，
// 那正是请求要纠正的局面。不带图面的请求（目标调整）无条件有效。
func TestLiveReplanReasons_DropsRequestsWhosePremiseChanged(t *testing.T) {
	a, store := revisionAgent(t, nil)
	ctx := context.Background()
	basis := basisOf(t, store)
	seen, err := store.Surface(ctx, revisionTask, basis)
	require.NoError(t, err)

	_, err = store.CreateNode(ctx, revisionNode("a-new", core.KindAction, explorationgraph.SourcePlanner, openState(), 5*time.Second))
	require.NoError(t, err)
	assert.Equal(t, []string{"原地打转"},
		a.liveReplanReasons(ctx, []replanRequest{{reason: "原地打转", seen: &seen}}),
		"只多了动作：请求仍成立")

	_, err = store.CreateNode(ctx, revisionNode("r-new", core.KindResult, explorationgraph.SourceEvaluator, nil, 6*time.Second))
	require.NoError(t, err)
	assert.Equal(t, []string{"目标已调整"}, a.liveReplanReasons(ctx, []replanRequest{
		{reason: "收益递减", seen: &seen},
		{reason: "目标已调整"},
	}), "出现新结果：基于旧图面的请求作废，无条件请求保留")
}
