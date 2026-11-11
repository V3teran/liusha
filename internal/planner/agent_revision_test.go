package planner

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

const revisionTask = "task-revision"

// analysisPlanner 返回固定分析结论的 GraphPlanner 替身，并记录被分析的结果。
type analysisPlanner struct {
	analysis *ResultAnalysis
	analyzed []string
}

func (p *analysisPlanner) Plan(context.Context, *explorationgraph.Store, string, []string) ([]explorationgraph.Node, error) {
	return nil, nil
}

func (p *analysisPlanner) AnalyzeResults(_ context.Context, _ *explorationgraph.Store, _ string, results []explorationgraph.Node) (*ResultAnalysis, error) {
	for _, r := range results {
		p.analyzed = append(p.analyzed, r.ID)
	}
	return p.analysis, nil
}

func isAnalyzed(t *testing.T, store *explorationgraph.Store, id string) bool {
	t.Helper()
	n, err := store.GetNode(context.Background(), id)
	require.NoError(t, err)
	return n.IsAnalyzed()
}

func revisionNode(id string, kind core.NodeKind, source explorationgraph.SourceType, state *explorationgraph.State, offset time.Duration) explorationgraph.Node {
	return explorationgraph.Node{
		ID: id, TaskID: revisionTask, Kind: kind, State: state, SourceType: source,
		Content:   []byte(`{"description":"` + id + `","summary":"` + id + `"}`),
		CreatedAt: time.Unix(1700000000, 0).Add(offset),
	}
}

func openState() *explorationgraph.State {
	s := explorationgraph.StateOpen
	return &s
}

// revisionAgent 构造带内存图的 agent：根目标 root、派生目标 d1/d2、结果 r1。
func revisionAgent(t *testing.T, analysis *ResultAnalysis) (*Agent, *explorationgraph.Store) {
	t.Helper()
	store := explorationgraph.NewMemoryStore()
	for _, n := range []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		revisionNode("d1", core.KindObjective, explorationgraph.SourcePlanner, openState(), time.Second),
		revisionNode("d2", core.KindObjective, explorationgraph.SourcePlanner, openState(), 2*time.Second),
		revisionNode("r1", core.KindResult, explorationgraph.SourceEvaluator, nil, 3*time.Second),
	} {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}
	return &Agent{
		taskID:   revisionTask,
		graph:    store,
		planner:  &analysisPlanner{analysis: analysis},
		eventBus: &fakeEventBus{},
		logger:   zerolog.Nop(),
	}, store
}

func stateIn(t *testing.T, store *explorationgraph.Store, id string) explorationgraph.State {
	t.Helper()
	n, err := store.GetNode(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, n.State)
	return *n.State
}

// 达成、新方向（含吸收）与延续动作同时出现时全部落实——此前有延续动作就提前
// 返回，新方向连同其 supersedes 声明被静默丢弃。
func TestAnalyzeAndProcessResults_AppliesEveryPartOfTheAnalysis(t *testing.T) {
	a, store := revisionAgent(t, &ResultAnalysis{
		CompletedObjectives: []explorationgraph.ObjectiveCompletion{{ObjectiveID: "d2", Evidence: []string{"r1"}}},
		NewObjectives: []NewObjective{{
			Description: "细化后的方向", Priority: core.PriorityHigh,
			TriggeredBy: []string{"r1"}, Supersedes: []string{"d1"},
		}},
		ContinuationActions: []ContinuationAction{{Instruction: "继续深挖参数", Priority: core.PriorityMedium}},
	})
	ctx := context.Background()

	require.NoError(t, a.analyzeNewResults(ctx))

	assert.Equal(t, explorationgraph.StateSuperseded, stateIn(t, store, "d1"))
	assert.Equal(t, explorationgraph.StateCompleted, stateIn(t, store, "d2"))
	assert.Equal(t, explorationgraph.StateOpen, stateIn(t, store, "root"), "根目标只随任务终态收敛")

	objectives, err := store.ListNodesByKind(ctx, revisionTask, core.KindObjective)
	require.NoError(t, err)
	require.Len(t, objectives, 4)
	added := objectives[3]
	assert.True(t, added.IsDerivedObjective())
	assert.Equal(t, explorationgraph.StateOpen, *added.State)

	edges, err := store.ListEdgesByTask(ctx, revisionTask)
	require.NoError(t, err)
	var supersedes, triggers int
	for _, e := range edges {
		switch {
		case e.Rel == explorationgraph.RelSupersedes && e.SrcID == added.ID && e.DstID == "d1":
			supersedes++
		case e.Rel == explorationgraph.RelTriggers && e.SrcID == "r1" && e.DstID == added.ID:
			triggers++
		}
	}
	assert.Equal(t, 1, supersedes)
	assert.Equal(t, 1, triggers)

	actions, err := store.ListNodesByKind(ctx, revisionTask, core.KindAction)
	require.NoError(t, err)
	assert.Len(t, actions, 1, "延续动作与目标修订一起落实")
	assert.True(t, isAnalyzed(t, store, "r1"), "分析水位随修订落图")
}

// 引用失效（幻觉 ID、根目标）只剔除该引用：新方向照常落图，分析水位照常推进。
func TestAnalyzeAndProcessResults_InvalidReferencesDoNotBlockRevision(t *testing.T) {
	a, store := revisionAgent(t, &ResultAnalysis{
		CompletedObjectives: []explorationgraph.ObjectiveCompletion{{ObjectiveID: "root", Evidence: []string{"r1"}}},
		NewObjectives: []NewObjective{{
			Description: "新方向", TriggeredBy: []string{"ghost-result"}, Supersedes: []string{"ghost", "root"},
		}},
	})
	ctx := context.Background()

	require.NoError(t, a.analyzeNewResults(ctx))

	assert.Equal(t, explorationgraph.StateOpen, stateIn(t, store, "root"))
	objectives, err := store.ListNodesByKind(ctx, revisionTask, core.KindObjective)
	require.NoError(t, err)
	assert.Len(t, objectives, 4, "新方向照常落图")
	assert.True(t, isAnalyzed(t, store, "r1"))
}

func TestObjectiveView_SeparatesRootLiveAndSettled(t *testing.T) {
	completed, superseded := explorationgraph.StateCompleted, explorationgraph.StateSuperseded
	objectives := []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		revisionNode("live", core.KindObjective, explorationgraph.SourcePlanner, openState(), time.Second),
		revisionNode("done", core.KindObjective, explorationgraph.SourcePlanner, &completed, 2*time.Second),
		revisionNode("absorbed", core.KindObjective, explorationgraph.SourcePlanner, &superseded, 3*time.Second),
	}

	section := buildObjectiveViewSection(newObjectiveView(objectives))

	assert.Contains(t, section, "## 任务目标\n\nroot")
	assert.Contains(t, section, "- `live` live", "进行中的派生目标带 ID 供引用")
	assert.NotContains(t, section, "`root`", "根目标不可引用")
	assert.Contains(t, section, "- [已达成] done")
	assert.Contains(t, section, "- [已吸收] absorbed")
	assert.NotContains(t, section, "`done`", "已收敛目标不带 ID")

	planning := buildObjectiveSection("任务", objectives)
	assert.Contains(t, planning, "### 进行中的派生目标\n- live")
	assert.False(t, strings.Contains(planning, "done") || strings.Contains(planning, "absorbed"),
		"规划只看进行中的方向：%s", planning)
}

// 修订整体失败（此处：没有根目标，延续动作无处归属）时什么都不落、水位不推进——
// 下一轮整体重做，不会重复写入上一轮已生效的一半。
func TestAnalyzeAndProcessResults_FailedRevisionWritesNothing(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	for _, n := range []explorationgraph.Node{
		revisionNode("d1", core.KindObjective, explorationgraph.SourcePlanner, openState(), 0),
		revisionNode("r1", core.KindResult, explorationgraph.SourceEvaluator, nil, time.Second),
	} {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}
	a := &Agent{
		taskID: revisionTask, graph: store, eventBus: &fakeEventBus{}, logger: zerolog.Nop(),
		planner: &analysisPlanner{analysis: &ResultAnalysis{
			NewObjectives:       []NewObjective{{Description: "新方向", Supersedes: []string{"d1"}}},
			ContinuationActions: []ContinuationAction{{Instruction: "深挖"}},
		}},
	}
	ctx := context.Background()

	require.Error(t, a.analyzeNewResults(ctx))

	assert.False(t, isAnalyzed(t, store, "r1"), "修订失败，水位随之未落")
	assert.Equal(t, explorationgraph.StateOpen, stateIn(t, store, "d1"))
	objectives, err := store.ListNodesByKind(ctx, revisionTask, core.KindObjective)
	require.NoError(t, err)
	assert.Len(t, objectives, 1, "新目标没有落下")
}

// 水位在图上：新 run（暂停后继续、崩溃重投）的 planner 不会把已分析的结果
// 再分析一遍，上一轮的新方向也不会被重复落图。
func TestAnalyzeNewResults_WatermarkSurvivesAcrossRuns(t *testing.T) {
	analysis := &ResultAnalysis{NewObjectives: []NewObjective{{Description: "新方向"}}}
	first, store := revisionAgent(t, analysis)
	ctx := context.Background()
	require.NoError(t, first.analyzeNewResults(ctx))

	resumedPlanner := &analysisPlanner{analysis: analysis}
	resumed := &Agent{
		taskID: revisionTask, graph: store, planner: resumedPlanner,
		eventBus: &fakeEventBus{}, logger: zerolog.Nop(),
	}
	require.NoError(t, resumed.analyzeNewResults(ctx))

	assert.Empty(t, resumedPlanner.analyzed, "已分析的结果不再送分析")
	objectives, err := store.ListNodesByKind(ctx, revisionTask, core.KindObjective)
	require.NoError(t, err)
	assert.Len(t, objectives, 4, "新方向只落一次")
}

// 延续动作与规划产出的动作守同一道去重：空指令与既有指令（含本批）字面相同的不落图。
func TestAnalyzeAndProcessResults_ContinuationActionsAreDeduplicated(t *testing.T) {
	a, store := revisionAgent(t, &ResultAnalysis{ContinuationActions: []ContinuationAction{
		{Instruction: "已做过的测试"},
		{Instruction: "  新测试  "},
		{Instruction: "新测试"},
		{Instruction: "   "},
	}})
	ctx := context.Background()
	_, err := store.CreateNode(ctx, revisionNode("a0", core.KindAction, explorationgraph.SourcePlanner, openState(), 5*time.Second))
	require.NoError(t, err)
	require.NoError(t, store.UpdateNodeContent(ctx, "a0", []byte(`{"instruction":"已做过的测试"}`)))

	require.NoError(t, a.analyzeNewResults(ctx))

	actions, err := store.ListNodesByKind(ctx, revisionTask, core.KindAction)
	require.NoError(t, err)
	require.Len(t, actions, 2, "只新增一条「新测试」")
	assert.JSONEq(t, `{"instruction":"新测试","reasoning":""}`, string(actions[1].Content))
}

// convergeRecorder 记录收敛事件的事件总线替身。
type convergeRecorder struct {
	fakeEventBus
	converged int
}

func (c *convergeRecorder) PublishTaskConverged(_, _ string) { c.converged++ }

// 收敛判定的唯一口径：未分析的结果、未裁决的观察与非终态动作一样阻止收敛，
// 常规规划与 monitor 强制重规划两条路径一致。
func TestHandleNoActions_PendingWorkBlocksConvergence(t *testing.T) {
	unverified := explorationgraph.ConfidenceUnverified
	cases := []struct {
		name    string
		seed    func(t *testing.T, store *explorationgraph.Store)
		blocked bool
	}{
		{"未分析结果", func(*testing.T, *explorationgraph.Store) {}, true},
		{"未裁决观察", func(t *testing.T, store *explorationgraph.Store) {
			markAllAnalyzed(t, store)
			obs := revisionNode("o1", core.KindObservation, explorationgraph.SourceExecutor, nil, 5*time.Second)
			obs.Confidence = &unverified
			_, err := store.CreateNode(context.Background(), obs)
			require.NoError(t, err)
		}, true},
		{"全部落定", func(t *testing.T, store *explorationgraph.Store) { markAllAnalyzed(t, store) }, false},
	}
	for _, tc := range cases {
		for _, trigger := range []string{"planning", "forced replan"} {
			t.Run(tc.name+"/"+trigger, func(t *testing.T) {
				a, store := revisionAgent(t, &ResultAnalysis{})
				bus := &convergeRecorder{}
				a.eventBus = bus
				tc.seed(t, store)

				a.handleNoActions(context.Background(), trigger)

				assert.Equal(t, !tc.blocked, bus.converged == 1)
			})
		}
	}
}

func markAllAnalyzed(t *testing.T, store *explorationgraph.Store) {
	t.Helper()
	_, err := store.RevisePlan(context.Background(), revisionTask, explorationgraph.PlanRevision{Analyzed: []string{"r1"}})
	require.NoError(t, err)
}

// 新目标与延续动作守同一道字面去重：与既有目标（含根目标）描述相同、空描述、
// 本批内重复的新目标都不落图。
func TestAnalyzeAndProcessResults_NewObjectivesAreDeduplicated(t *testing.T) {
	a, store := revisionAgent(t, &ResultAnalysis{NewObjectives: []NewObjective{
		{Description: "d1"},     // 既有派生目标
		{Description: " root "}, // 根目标
		{Description: "  "},
		{Description: "新方向"},
		{Description: "新方向 "},
	}})
	ctx := context.Background()

	require.NoError(t, a.analyzeNewResults(ctx))

	objectives, err := store.ListNodesByKind(ctx, revisionTask, core.KindObjective)
	require.NoError(t, err)
	require.Len(t, objectives, 4, "只新增一条「新方向」")
	assert.Equal(t, "新方向", objectiveDescription(objectives[3]))
	assert.True(t, isAnalyzed(t, store, "r1"), "分析水位随修订照常落图")
}

func planAction(id, instruction string) explorationgraph.Node {
	n := revisionNode(id, core.KindAction, explorationgraph.SourcePlanner, openState(), 10*time.Second)
	n.Content = []byte(`{"instruction":"` + instruction + `"}`)
	return n
}

// 规划入口与结果分析、人工注入同一口径：动作挂根目标；空指令与重复指令不落图。
func TestWriteActionsFromPlan_HangsOnRootAndSkipsBlankAndRepeats(t *testing.T) {
	a, store := revisionAgent(t, &ResultAnalysis{})
	ctx := context.Background()

	require.NoError(t, a.writeActionsFromPlan(ctx, []explorationgraph.Node{
		planAction("p1", "探测登录接口"),
		planAction("p2", " 探测登录接口 "),
		planAction("p3", "  "),
	}))

	actions, err := store.ListNodesByKind(ctx, revisionTask, core.KindAction)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	assert.Equal(t, "p1", actions[0].ID)
	edges, err := store.ListEdgesByTask(ctx, revisionTask)
	require.NoError(t, err)
	require.Len(t, edges, 1)
	assert.Equal(t, [3]string{"root", string(explorationgraph.RelGenerates), "p1"},
		[3]string{edges[0].SrcID, string(edges[0].Rel), edges[0].DstID})
}

func TestWriteActionsFromPlan_RequiresRootObjective(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	a := &Agent{taskID: revisionTask, graph: store, eventBus: &fakeEventBus{}, logger: zerolog.Nop()}
	ctx := context.Background()

	require.Error(t, a.writeActionsFromPlan(ctx, []explorationgraph.Node{planAction("p1", "x")}))
	actions, err := store.ListNodesByKind(ctx, revisionTask, core.KindAction)
	require.NoError(t, err)
	assert.Empty(t, actions, "无根目标不落孤立动作")
}
