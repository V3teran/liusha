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

// scriptedPlanner 按调用次序给出规划结论，并记录调用次数（超出脚本即重复最后一条）。
type scriptedPlanner struct {
	outcomes []PlanOutcome
	calls    int
}

func (p *scriptedPlanner) Plan(context.Context, *explorationgraph.Store, string, PlanRequest) (PlanOutcome, error) {
	p.calls++
	i := min(p.calls, len(p.outcomes)) - 1
	return p.outcomes[i], nil
}

func (p *scriptedPlanner) AnalyzeResults(context.Context, *explorationgraph.Store, string, []explorationgraph.Node) (*ResultAnalysis, error) {
	return &ResultAnalysis{}, nil
}

func stateRef(s explorationgraph.State) *explorationgraph.State { return &s }

// incidentStore 复现停止探索时的局面：根目标；规划者的执行中动作 p-run；用户指派的执行中
// 动作 u-run（按参数）；一条待验证假设 h1。
func incidentStore(t *testing.T, withUserAction bool) *explorationgraph.Store {
	t.Helper()
	store := explorationgraph.NewMemoryStore()
	pending := explorationgraph.VerificationPending
	h1 := revisionNode("h1", core.KindObservation, explorationgraph.SourceExecutor, nil, 2*time.Second)
	h1.VerificationStatus = &pending
	nodes := []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		revisionNode("p-run", core.KindAction, explorationgraph.SourcePlanner, stateRef(explorationgraph.StateRunning), time.Second),
		h1,
	}
	if withUserAction {
		nodes = append(nodes, revisionNode("u-run", core.KindAction, explorationgraph.SourceUser, openState(), 3*time.Second))
	}
	for _, n := range nodes {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}
	if withUserAction {
		ok, err := store.ClaimAction(context.Background(), revisionTask, "u-run", "run-1")
		require.NoError(t, err)
		require.True(t, ok)
	}
	return store
}

func settleHypothesis(t *testing.T, store *explorationgraph.Store, id string, status explorationgraph.VerificationStatus) {
	t.Helper()
	require.NoError(t, store.UpdateNodeMetadata(context.Background(), id, explorationgraph.MustJSON(map[string]string{"verification_status": string(status)})))
}

func conclusionAgent(store *explorationgraph.Store, p GraphPlanner, converged *int) *Agent {
	return &Agent{taskID: revisionTask, graph: store, planner: p, eventBus: &fakeEventBus{}, logger: zerolog.Nop(),
		onConverged: func(string, explorationgraph.GoalBasis) { *converged++ }}
}

// 规划者判停时仍有执行中的规划动作：判停是对任务的结论，收回它们，而不是等它们跑完；
// 之后图面不变（包括收回的探索留下的假设被证伪）不再调用 LLM，假设落定即收敛。
func TestPlanActions_StopWithdrawsInFlightWorkAndDoesNotResolve(t *testing.T) {
	store := incidentStore(t, false)
	p := &scriptedPlanner{outcomes: []PlanOutcome{{Stop: true}}}
	converged := 0
	a := conclusionAgent(store, p, &converged)
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))
	n, err := store.GetNode(ctx, "p-run")
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateAborted, *n.State, "判停收回执行中的规划动作")
	assert.Zero(t, converged, "待验证假设未落定，不收敛")

	// 轮询与验证事件反复唤醒：图面不变，复用判停结论
	require.NoError(t, a.planActions(ctx))
	settleHypothesis(t, store, "h1", explorationgraph.VerificationRefuted)
	require.NoError(t, a.planActions(ctx))

	assert.Equal(t, 1, p.calls, "判停之后图面未长出新内容，不再求解")
	assert.Equal(t, 1, converged, "余下工作落定即收敛")
}

// 用户指派的动作不随判停收回：它做完之前不收敛，做完之后（动作结束不使判停失效）收敛。
func TestPlanActions_StopKeepsUserAssignedWork(t *testing.T) {
	store := incidentStore(t, true)
	settleHypothesis(t, store, "h1", explorationgraph.VerificationRefuted)
	p := &scriptedPlanner{outcomes: []PlanOutcome{{Stop: true}}}
	converged := 0
	a := conclusionAgent(store, p, &converged)
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))
	n, err := store.GetNode(ctx, "u-run")
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateRunning, *n.State, "用户指派的动作照常做完")
	assert.Zero(t, converged)

	ok, err := store.FinishAction(ctx, revisionTask, "u-run", "run-1", explorationgraph.StateCompleted, "")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, a.planActions(ctx))

	assert.Equal(t, 1, p.calls)
	assert.Equal(t, 1, converged)
}

// 判停之后图上出现了新的发现（新结果）：判停时没有的信息，重新求解。
func TestPlanActions_StopIsReopenedByNewResult(t *testing.T) {
	store := incidentStore(t, false)
	p := &scriptedPlanner{outcomes: []PlanOutcome{{Stop: true}}}
	converged := 0
	a := conclusionAgent(store, p, &converged)
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))
	_, err := store.CreateNode(ctx, revisionNode("r1", core.KindResult, explorationgraph.SourceEvaluator, nil, 5*time.Second))
	require.NoError(t, err)
	require.NoError(t, a.planActions(ctx))

	assert.Equal(t, 2, p.calls)
}

// 「继续但本轮没有新动作」是对当前图面的等待：图面不变不重复求解，在途动作结束即重新求解。
func TestPlanActions_WaitIsReusedUntilProgress(t *testing.T) {
	store := incidentStore(t, false)
	p := &scriptedPlanner{outcomes: []PlanOutcome{{}}}
	converged := 0
	a := conclusionAgent(store, p, &converged)
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))
	require.NoError(t, a.planActions(ctx))
	assert.Equal(t, 1, p.calls, "图面不变，复用等待结论")
	n, err := store.GetNode(ctx, "p-run")
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateRunning, *n.State, "等待不收回在途动作")

	settleHypothesis(t, store, "h1", explorationgraph.VerificationRefuted)
	require.NoError(t, a.planActions(ctx))
	assert.Equal(t, 2, p.calls, "新的验证结论是进展，重新求解")
	assert.Zero(t, converged)
}

// 目标被改写使任何结论失效。
func TestPlanActions_GoalChangeInvalidatesConclusion(t *testing.T) {
	store := incidentStore(t, false)
	p := &scriptedPlanner{outcomes: []PlanOutcome{{Stop: true}}}
	converged := 0
	a := conclusionAgent(store, p, &converged)
	ctx := context.Background()

	require.NoError(t, a.planActions(ctx))
	require.NoError(t, store.UpdateNodeContent(ctx, "root", explorationgraph.MustJSON(map[string]string{"description": "调整后的目标"})))
	require.NoError(t, a.planActions(ctx))

	assert.Equal(t, 2, p.calls)
}
