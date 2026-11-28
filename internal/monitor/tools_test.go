package monitor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/monitor/metrics"
)

// monitor 的两个工具是监察决策的出入口：
// GetGlobalStateTool 汇总探索图态势；PublishDecisionTool 的 reject_action
// 直接把动作置为已否决（执行者只认领 open、在途执行随状态变化中断，即真实生效路径）。

func newGraphWithActions(t *testing.T) *explorationgraph.Store {
	t.Helper()
	graph := explorationgraph.NewMemoryStore()
	ctx := context.Background()

	obj, err := graph.CreateNode(ctx, explorationgraph.Node{
		ID: "obj-1", TaskID: "t1", Kind: core.KindObjective,
		Content: []byte(`{"description":"测试目标"}`),
	})
	require.NoError(t, err)

	state := explorationgraph.StateOpen
	for _, id := range []string{"a-done", "a-open", "a-running"} {
		st := state
		content := []byte(`{"instruction":"do something"}`)
		if id == "a-done" {
			done := explorationgraph.StateCompleted
			st = done
			content = []byte(`{"instruction":"done thing"}`)
		}
		if _, err := graph.CreateNode(ctx, explorationgraph.Node{
			ID: id, TaskID: "t1", Kind: core.KindAction,
			Content: content, State: &st,
		}); err != nil {
			t.Fatalf("create action %s: %v", id, err)
		}
	}
	_ = obj
	return graph
}

func TestGetGlobalStateTool_SumsExplorationState(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := context.Background()

	// 创建 metrics collector
	logger := zerolog.New(zerolog.NewTestWriter(t))
	metricsCollector := metrics.NewCollector(graph, "t1", logger)

	tool := NewGetGlobalStateTool(graph, "t1", metricsCollector)
	out, err := tool.Execute(ctx, []byte(`{}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)

	var state struct {
		Objective ObjectiveView           `json:"objective"`
		Actions   []explorationgraph.Node `json:"actions"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.Output), &state))
	assert.Len(t, state.Actions, 3, "三个 action 全量返回")
	assert.NotEmpty(t, state.Objective.ID, "根目标应存在")
}

// 监察者判断「方向已被证伪」「最近提交的假设全部被证伪」「没有待验证的假设」都要看假设：
// 全局状态按提交先后列出假设的陈述、验证状态与反驳原因；事实观察不在其列。
func TestGetGlobalStateTool_ListsHypotheses(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := context.Background()
	base := time.Unix(1700000000, 0)
	pending := explorationgraph.VerificationPending
	refuted := explorationgraph.VerificationRefuted
	for _, n := range []explorationgraph.Node{
		{ID: "h-refuted", TaskID: "t1", Kind: core.KindObservation, VerificationStatus: &refuted,
			Content:   []byte(`{"category":"hypothesis","statement":"主张甲"}`),
			Metadata:  []byte(`{"refute_reason":"claim_invalid"}`),
			CreatedAt: base.Add(time.Second)},
		{ID: "h-pending", TaskID: "t1", Kind: core.KindObservation, VerificationStatus: &pending,
			Content: []byte(`{"category":"hypothesis","statement":"主张乙"}`), CreatedAt: base.Add(2 * time.Second)},
		{ID: "fact", TaskID: "t1", Kind: core.KindObservation,
			Content: []byte(`{"category":"entity","statement":"事实"}`), CreatedAt: base.Add(3 * time.Second)},
	} {
		_, err := graph.CreateNode(ctx, n)
		require.NoError(t, err)
	}

	tool := NewGetGlobalStateTool(graph, "t1", metrics.NewCollector(graph, "t1", zerolog.Nop()))
	out, err := tool.Execute(ctx, []byte(`{}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)

	var state struct {
		Hypotheses []HypothesisView `json:"hypotheses"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.Output), &state))
	require.Len(t, state.Hypotheses, 2, "只列假设，不列事实")
	assert.Equal(t, HypothesisView{ID: "h-refuted", Statement: "主张甲", VerificationStatus: refuted,
		RefuteReason: "claim_invalid", CreatedAt: state.Hypotheses[0].CreatedAt}, state.Hypotheses[0])
	assert.Equal(t, "h-pending", state.Hypotheses[1].ID)
	assert.Equal(t, pending, state.Hypotheses[1].VerificationStatus)
	assert.Empty(t, state.Hypotheses[1].RefuteReason)
}

// 监察者判断「线索堆积着没人深挖」「追了总做不完」要看着等跟进的线索：全局状态按创建
// 先后列出它们，带挂了多久与没做完、被否决的次数；被动作跟进的、已关闭的与普通事实都
// 不在其列。
func TestGetGlobalStateTool_ListsOpenLeads(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := context.Background()
	base := time.Now().Add(-30 * time.Minute)
	lead := func(id, statement string, age time.Duration) explorationgraph.Node {
		return explorationgraph.Node{ID: id, TaskID: "t1", Kind: core.KindObservation, SourceType: explorationgraph.SourceExecutor,
			Content:   []byte(`{"category":"access","statement":"` + statement + `"}`),
			Lead:      &explorationgraph.LeadSpec{Next: "顺着它再走一步", Question: "结论是什么", Potential: "一条可坐实的主张"},
			CreatedAt: base.Add(age)}
	}
	for _, n := range []explorationgraph.Node{
		lead("lead-open", "待深挖的线索", 0),
		lead("lead-held", "被跟进的线索", time.Minute),
		{ID: "lead-shut", TaskID: "t1", Kind: core.KindObservation, SourceType: explorationgraph.SourceExecutor,
			Content:    []byte(`{"category":"access","statement":"已关闭的线索"}`),
			Lead:       &explorationgraph.LeadSpec{Next: "n", Question: "q", Potential: "p"},
			LeadClosed: &explorationgraph.LeadClosure{Kind: explorationgraph.ClosureOutOfScope, Reason: "超出任务目标"}},
		{ID: "fact", TaskID: "t1", Kind: core.KindObservation,
			Content: []byte(`{"category":"access","statement":"普通事实"}`)},
	} {
		_, err := graph.CreateNode(ctx, n)
		require.NoError(t, err)
	}
	open := explorationgraph.StateOpen
	require.NoError(t, graph.CreateNodeWithEdges(ctx, explorationgraph.Node{
		ID: "a-pursuing", TaskID: "t1", Kind: core.KindAction, State: &open, SourceType: explorationgraph.SourcePlanner,
		Content: []byte(`{"instruction":"深挖"}`),
	}, []explorationgraph.Edge{{TaskID: "t1", SrcID: "a-pursuing", Rel: explorationgraph.RelPursues, DstID: "lead-held"}}))
	// lead-open 先被否决过一次，之后两次跟进都没做完（迭代预算耗尽）：处境由图推导，监察视图
	// 带出来。
	for i, id := range []string{"a-vetoed", "a-cut-1", "a-cut-2"} {
		state := explorationgraph.StateExhausted
		if id == "a-vetoed" {
			state = explorationgraph.StateRejected
		}
		require.NoError(t, graph.CreateNodeWithEdges(ctx, explorationgraph.Node{
			ID: id, TaskID: "t1", Kind: core.KindAction, State: &state, SourceType: explorationgraph.SourcePlanner,
			Content: []byte(`{"instruction":"深挖"}`), UpdatedAt: base.Add(time.Duration(i+1) * time.Minute),
		}, []explorationgraph.Edge{{TaskID: "t1", SrcID: id, Rel: explorationgraph.RelPursues, DstID: "lead-open"}}))
	}

	tool := NewGetGlobalStateTool(graph, "t1", metrics.NewCollector(graph, "t1", zerolog.Nop()))
	out, err := tool.Execute(ctx, []byte(`{}`))
	require.NoError(t, err)
	require.Empty(t, out.Error)

	var state struct {
		OpenLeads []OpenLead `json:"open_leads"`
	}
	require.NoError(t, json.Unmarshal([]byte(out.Output), &state))
	require.Len(t, state.OpenLeads, 1, "只有没人跟进、没被关闭的线索待深挖")
	got := state.OpenLeads[0]
	assert.Equal(t, "lead-open", got.ID)
	assert.InDelta(t, 30, got.AgeMinutes, 0.2, "挂了约 30 分钟")
	assert.Equal(t, 2, got.Truncated)
	assert.Equal(t, 2, got.TruncationStreak)
	assert.Equal(t, 1, got.Vetoes)
}

func TestPublishDecisionTool_RejectAction_AppliesStateChange(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := observedCtx(t, graph, "t1")
	tool := NewPublishDecisionTool(graph, &replanRecorder{}, "t1")

	out, err := tool.Execute(ctx, decisionArgs(t, Decision{
		Type: DecisionRejectAction, ActionID: "a-running", Problem: ProblemDuplicate,
		EvidenceIDs: []string{"a-done"}, Reason: "与已完成动作做同一件事",
	}))
	require.NoError(t, err)
	require.Empty(t, out.Error)

	node, err := graph.GetNode(context.Background(), "a-running")
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateRejected, *node.State)
	assert.Equal(t, explorationgraph.SourceMonitor, node.RejectedBy())
	assert.Equal(t, "重复：与已完成动作做同一件事（证据：a-done）", *node.StateReason)
}

// 否决带范围：重复只否决这一步，它跟进的线索带着否决理由重新待深挖；偏离目标评判的是
// 这条线本身，被否决的动作所跟进的线索随之关闭。
func TestPublishDecisionTool_RejectAction_ScopeSettlesThePursuedLead(t *testing.T) {
	graph := newGraphWithActions(t)
	ctx := context.Background()
	running := explorationgraph.StateRunning
	_, err := graph.CreateNode(ctx, explorationgraph.Node{ID: "lead-1", TaskID: "t1", Kind: core.KindObservation,
		SourceType: explorationgraph.SourceExecutor, Content: []byte(`{"category":"access","statement":"探通一条路"}`),
		Lead: &explorationgraph.LeadSpec{Next: "n", Question: "q", Potential: "p"}})
	require.NoError(t, err)
	pursue := func(id string) {
		require.NoError(t, graph.CreateNodeWithEdges(ctx, explorationgraph.Node{
			ID: id, TaskID: "t1", Kind: core.KindAction, State: &running, SourceType: explorationgraph.SourcePlanner,
			Content: []byte(`{"instruction":"深挖"}`),
		}, []explorationgraph.Edge{{TaskID: "t1", SrcID: id, Rel: explorationgraph.RelPursues, DstID: "lead-1"}}))
	}
	pursue("a-pursuit-1")
	tool := NewPublishDecisionTool(graph, &replanRecorder{}, "t1")

	out, err := tool.Execute(observedCtx(t, graph, "t1"), decisionArgs(t, Decision{
		Type: DecisionRejectAction, ActionID: "a-pursuit-1", Problem: ProblemDuplicate,
		EvidenceIDs: []string{"a-done"}, Reason: "与已完成的动作做同一件事",
	}))
	require.NoError(t, err)
	require.Empty(t, out.Error)
	node, err := graph.GetNode(ctx, "a-pursuit-1")
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.RejectStep, node.RejectionScope(), "重复只否决这一步")
	open, err := graph.OpenLeads(ctx, "t1")
	require.NoError(t, err)
	require.Len(t, open, 1, "线索带着否决理由重新待深挖")
	assert.Equal(t, 1, open[0].Vetoes)
	assert.Contains(t, open[0].LastVetoReason, "重复：与已完成的动作做同一件事")

	pursue("a-pursuit-2")
	out, err = tool.Execute(observedCtx(t, graph, "t1"), decisionArgs(t, Decision{
		Type: DecisionRejectAction, ActionID: "a-pursuit-2", Problem: ProblemOffGoal,
		EvidenceIDs: []string{"a-pursuit-2"}, Reason: "对象不在任务目标范围内",
	}))
	require.NoError(t, err)
	require.Empty(t, out.Error)
	node, err = graph.GetNode(ctx, "a-pursuit-2")
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.RejectLead, node.RejectionScope(), "偏离目标连线索一起评判")
	closed, err := graph.ClosedLeads(ctx, "t1")
	require.NoError(t, err)
	require.Len(t, closed, 1, "线索随之关闭")
	assert.Equal(t, explorationgraph.ClosureVetoed, closed[0].Closure.Kind)
	assert.Contains(t, closed[0].Closure.Reason, "偏离目标：对象不在任务目标范围内")
}

// 用户指派的动作体现人的明确意图，监察者无权否决；状态不得改变。
func TestPublishDecisionTool_RejectAction_SparesUserAssigned(t *testing.T) {
	graph := newGraphWithActions(t)
	open := explorationgraph.StateOpen
	_, err := graph.CreateNode(context.Background(), explorationgraph.Node{
		ID: "a-user", TaskID: "t1", Kind: core.KindAction, State: &open,
		Content: []byte(`{"instruction":"用户指定的一步"}`), SourceType: explorationgraph.SourceUser,
	})
	require.NoError(t, err)
	ctx := observedCtx(t, graph, "t1")
	tool := NewPublishDecisionTool(graph, &replanRecorder{}, "t1")

	out, err := tool.Execute(ctx, decisionArgs(t, Decision{
		Type: DecisionRejectAction, ActionID: "a-user", Problem: ProblemOffGoal, EvidenceIDs: []string{"a-user"}, Reason: "偏离",
	}))
	require.NoError(t, err)
	assert.Contains(t, out.Error, "用户指派")
	node, err := graph.GetNode(context.Background(), "a-user")
	require.NoError(t, err)
	assert.Equal(t, explorationgraph.StateOpen, *node.State)
}

// running_actions 时长视图是 monitor kill 决策的变量源（此前只给原始时间戳，
// LLM 无从计算时长，20 分钟 kill 职责空转——e2e 实测 action 跑 30+ 分钟无人杀）。
func TestRunningActionViews(t *testing.T) {
	now := time.Now()
	running := explorationgraph.StateRunning
	done := explorationgraph.StateCompleted
	mk := func(id string, st explorationgraph.State, ageMin float64) explorationgraph.Node {
		return explorationgraph.Node{
			ID: id, Kind: core.KindAction, State: &st,
			Content:   json.RawMessage(`{"instruction":"做某事"}`),
			UpdatedAt: now.Add(-time.Duration(ageMin * float64(time.Minute))),
		}
	}
	actions := []explorationgraph.Node{
		mk("a-running-30m", running, 30),
		mk("b-running-2m", running, 2),
		mk("c-done", done, 99),      // 非 running 不进视图
		mk("d-future", running, -5), // 时钟偏移防御：负时长归零
	}

	views := (&GetGlobalStateTool{}).runningActionViews(actions, now)
	if len(views) != 3 {
		t.Fatalf("应只含 3 个 running 动作, got %d", len(views))
	}
	if views[0].ID != "a-running-30m" || views[0].RunningMinutes < 29.9 || views[0].RunningMinutes > 30.1 {
		t.Fatalf("30 分钟动作时长应正确计算, got %+v", views[0])
	}
	if views[1].RunningMinutes < 1.9 || views[1].RunningMinutes > 2.1 {
		t.Fatalf("2 分钟动作时长应正确计算, got %+v", views[1])
	}
	if views[2].RunningMinutes != 0 {
		t.Fatalf("时钟偏移应归零, got %+v", views[2])
	}
	if views[0].Instruction != "做某事" {
		t.Fatalf("视图应携带 instruction 供决策上下文, got %+v", views[0])
	}
}
