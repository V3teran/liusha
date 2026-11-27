package explorationgraph

import (
	"context"
	"encoding/json"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

var leadBase = time.Unix(1700000000, 0)

// factLead 建一条标了 opens_deeper 的事实观察（无验证状态=事实）。
func factLead(id string) Node {
	return Node{
		ID: id, TaskID: revTask, Kind: core.KindObservation, SourceType: SourceExecutor,
		Content: []byte(`{"category":"access","statement":"探通了一条路"}`), Metadata: OpensDeeperMetadata(),
		CreatedAt: leadBase,
	}
}

// seedLead 建一条线索，创建时间依次靠后（处境按创建先后排列）。
func seedLead(t *testing.T, store *Store, id string) {
	t.Helper()
	leadSeq++
	n := factLead(id)
	n.CreatedAt = leadBase.Add(time.Duration(leadSeq) * time.Second)
	seedNode(t, store, n)
}

var leadSeq int

// observation 建一条观察：category 为 hypothesis 即假设（带验证状态），否则是事实。
func observation(id, category, statement string) Node {
	content, _ := json.Marshal(map[string]string{"category": category, "statement": statement})
	n := Node{ID: id, TaskID: revTask, Kind: core.KindObservation, SourceType: SourceExecutor, Content: content}
	if ObservationCategory(category) == CategoryHypothesis {
		pending := VerificationPending
		n.VerificationStatus = &pending
	}
	return n
}

// generatedBy 建一组 action → 观察的 generates 边。
func generatedBy(actionID string, observations ...Node) []Edge {
	edges := make([]Edge, 0, len(observations))
	for _, obs := range observations {
		edges = append(edges, Edge{TaskID: revTask, SrcID: actionID, Rel: RelGenerates, DstID: obs.ID})
	}
	return edges
}

// pursuitEdge 建一条 action → 线索的跟进边（动作认领这条线索）。
func pursuitEdge(actionID, leadID string) Edge {
	return Edge{TaskID: revTask, SrcID: actionID, Rel: RelPursues, DstID: leadID}
}

// plannedAction 建一个规划者排出的、处在给定状态的动作。
func plannedAction(id string, state State) Node {
	return Node{
		ID: id, TaskID: revTask, Kind: core.KindAction, State: stateOf(state),
		SourceType: SourcePlanner, Content: []byte(`{"instruction":"深挖"}`),
	}
}

// endedAt 把动作的最近一次状态迁移时刻设为 base 之后 offset。
func endedAt(n Node, offset time.Duration) Node {
	n.UpdatedAt = leadBase.Add(offset)
	return n
}

// OpensDeeper 只认事实观察：事实标了即 true；假设即便 metadata 带 opens_deeper 也不是
// 线索（假设在复现门里推进，不走线索通道）；没标的事实不是线索。
func TestNode_OpensDeeper(t *testing.T) {
	l1 := factLead("l1")
	assert.True(t, l1.OpensDeeper(), "标了 opens_deeper 的事实")

	plain := Node{ID: "l2", Kind: core.KindObservation, Content: []byte(`{"category":"access","statement":"x"}`)}
	assert.False(t, plain.OpensDeeper(), "没标 opens_deeper 不是线索")

	pending := VerificationPending
	hypo := Node{ID: "h1", Kind: core.KindObservation, VerificationStatus: &pending,
		Metadata: OpensDeeperMetadata(), Content: []byte(`{"category":"hypothesis","statement":"s"}`)}
	assert.False(t, hypo.OpensDeeper(), "假设即便带 opens_deeper 也不是线索")
}

// 规划层的关闭批注从线索上读：类别与理由都在；没有批注、批注损坏都按「没有」处理，不 panic。
func TestNode_LeadClosure(t *testing.T) {
	lead := factLead("l1")
	_, ok := lead.LeadClosure()
	assert.False(t, ok, "没有批注")

	lead.Metadata, _ = json.Marshal(map[string]interface{}{
		"opens_deeper": true, "lead_closed": map[string]interface{}{"kind": "out_of_scope", "reason": "超出任务目标"},
	})
	closure, ok := lead.LeadClosure()
	require.True(t, ok)
	assert.Equal(t, LeadClosure{Kind: ClosureOutOfScope, Reason: "超出任务目标"}, closure)

	lead.Metadata = json.RawMessage(`{"lead_closed":"损坏的批注"}`)
	_, ok = lead.LeadClosure()
	assert.False(t, ok, "批注形状不对")

	lead.Metadata = json.RawMessage(`not json`)
	_, ok = lead.LeadClosure()
	assert.False(t, ok, "metadata 解析失败")
}

// 只有规划者判定的类别能进规划修订；由结局推导的类别（有结论、推进、干涸、被否决）不行。
func TestLeadClosureKind_Declared(t *testing.T) {
	for _, k := range []LeadClosureKind{ClosureNotWorth, ClosureOutOfScope, ClosurePremiseRefuted, ClosureExhausted, ClosureBlocked} {
		assert.True(t, k.Declared(), string(k))
	}
	for _, k := range []LeadClosureKind{ClosureConcluded, ClosureDry, ClosureVetoed, "made_up"} {
		assert.False(t, k.Declared(), string(k))
	}
}

// 一次跟进的结局按停止原因归类，不按「动作跑完了没有」一刀切：报出了主张即有结论，关闭；
// 没有主张但留下了新东西即推进，线索仍待深挖；什么都没留下即干涸，关闭；没做完（预算耗尽）、
// 故障、收回都不计好坏，线索重新待深挖；否决按范围结算。
func TestDeriveLeads_OutcomeFollowsStopReason(t *testing.T) {
	claim := observation("claim", "hypothesis", "提交后整库被重置")
	fact := observation("fact", "access", "提交需要页面里的 token")
	note := observation("note", "note", "执行总结：完成")
	sideLead := factLead("side-lead")
	untouched := func(t *testing.T, lead Lead) {
		t.Helper()
		assert.Zero(t, lead.Truncated)
		assert.Zero(t, lead.Faults)
		assert.Zero(t, lead.Vetoes)
		assert.Zero(t, lead.Progressed)
	}
	cases := []struct {
		name         string
		state        *State      // nil = 没有动作跟进
		scope        RejectScope // rejected 时的否决范围
		observations []Node      // 跟进动作产出的观察
		others       []Node      // 别的动作先报出的观察（同一陈述）
		wantPending  bool
		wantClosure  LeadClosureKind // 空 = 开着
		check        func(t *testing.T, lead Lead)
	}{
		{name: "没有动作跟进：待深挖", wantPending: true, check: untouched},
		{name: "open 跟进：在办", state: stateOf(StateOpen), check: func(t *testing.T, lead Lead) {
			assert.True(t, lead.InFlight)
		}},
		{name: "running 跟进：在办", state: stateOf(StateRunning), check: func(t *testing.T, lead Lead) {
			assert.True(t, lead.InFlight)
		}},
		{name: "completed 且报出了主张：有结论", state: stateOf(StateCompleted), observations: []Node{claim},
			wantClosure: ClosureConcluded},
		{name: "completed 且留下了新事实：推进了，仍待深挖", state: stateOf(StateCompleted), observations: []Node{fact},
			wantPending: true, check: func(t *testing.T, lead Lead) { assert.Equal(t, 1, lead.Progressed) }},
		{name: "completed 且留下了新线索：推进了，仍待深挖", state: stateOf(StateCompleted), observations: []Node{sideLead},
			wantPending: true, check: func(t *testing.T, lead Lead) { assert.Equal(t, 1, lead.Progressed) }},
		{name: "completed 且只留下杂记：干涸", state: stateOf(StateCompleted), observations: []Node{note},
			wantClosure: ClosureDry},
		{name: "completed 且什么观察都没有：干涸", state: stateOf(StateCompleted), wantClosure: ClosureDry},
		{name: "completed 但它报的事实别的动作已经报过：干涸", state: stateOf(StateCompleted),
			observations: []Node{fact}, others: []Node{observation("earlier", "access", "提交需要页面里的 token")},
			wantClosure: ClosureDry},
		{name: "exhausted：没做完，重新待深挖", state: stateOf(StateExhausted), wantPending: true,
			check: func(t *testing.T, lead Lead) {
				assert.Equal(t, 1, lead.Truncated)
				assert.Equal(t, 1, lead.TruncationStreak)
				assert.Zero(t, lead.Faults)
			}},
		{name: "failed：故障，重新待深挖", state: stateOf(StateFailed), wantPending: true,
			check: func(t *testing.T, lead Lead) {
				assert.Equal(t, 1, lead.Faults)
				assert.Zero(t, lead.Truncated)
			}},
		{name: "aborted：收回，什么都不计", state: stateOf(StateAborted), wantPending: true, check: untouched},
		{name: "rejected 只否决这一步：带着理由重新待深挖", state: stateOf(StateRejected), scope: RejectStep,
			wantPending: true, check: func(t *testing.T, lead Lead) {
				assert.Equal(t, 1, lead.Vetoes)
				assert.Equal(t, "重复：已在动作甲里做过", lead.LastVetoReason)
			}},
		{name: "rejected 连线索一起：关闭", state: stateOf(StateRejected), scope: RejectLead,
			wantClosure: ClosureVetoed, check: func(t *testing.T, lead Lead) {
				require.NotNil(t, lead.Closure)
				assert.Equal(t, "偏离目标：对象不在任务范围内", lead.Closure.Reason)
			}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			nodes := []Node{factLead("lead"), claim, fact, note, sideLead}
			nodes = append(nodes, c.others...)
			actions := []Node{plannedAction("other", StateCompleted)}
			edges := generatedBy("other", c.others...)
			if c.state != nil {
				action := plannedAction("act", *c.state)
				if *c.state == StateRejected {
					reason := "偏离目标：对象不在任务范围内"
					if c.scope == RejectStep {
						reason = "重复：已在动作甲里做过"
					}
					action.StateReason = &reason
					action.Metadata, _ = json.Marshal(map[string]string{"rejected_scope": string(c.scope)})
				}
				actions = append(actions, action)
				edges = append(edges, generatedBy("act", c.observations...)...)
				edges = append(edges, pursuitEdge("act", "lead"))
			}

			leads := deriveLeads(nodes, actions, edges)
			require.Len(t, leads, 2, "原线索与跟进报出的新线索各一条")
			lead := leads[0]
			require.Equal(t, "lead", lead.ID)

			if c.wantClosure == "" {
				assert.Nil(t, lead.Closure, "线索开着")
			} else {
				require.NotNil(t, lead.Closure, "线索已关闭")
				assert.Equal(t, c.wantClosure, lead.Closure.Kind)
			}
			assert.Equal(t, c.wantPending, lead.Pending(), "是否待深挖")
			if c.check != nil {
				c.check(t, lead)
			}
		})
	}
}

// 同一条线索累计被否决两次（都只否决这一步）即关闭：否决与重开不再来回打转，关闭理由带
// 最近一次否决的说法。
func TestDeriveLeads_ClosesAfterSecondVeto(t *testing.T) {
	actions := []Node{endedAt(plannedAction("v1", StateRejected), time.Minute), endedAt(plannedAction("v2", StateRejected), 2*time.Minute)}
	edges := []Edge{pursuitEdge("v1", "lead"), pursuitEdge("v2", "lead")}
	for i := range actions {
		reason := "重复：已在动作甲里做过"
		actions[i].StateReason = &reason
		actions[i].Metadata, _ = json.Marshal(map[string]string{"rejected_scope": string(RejectStep)})
	}

	leads := deriveLeads([]Node{factLead("lead")}, actions, edges)
	require.Len(t, leads, 1)
	require.NotNil(t, leads[0].Closure)
	assert.Equal(t, ClosureVetoed, leads[0].Closure.Kind)
	assert.Contains(t, leads[0].Closure.Reason, "累计被否决 2 次")
}

// 连续没做完的次数自最近一次已了结的跟进向前数：被收回的不算了结、不打断计数；最近一次
// 已了结的跟进以别的方式收场即归零。
func TestDeriveLeads_TruncationStreak(t *testing.T) {
	build := func(last State) []Lead {
		actions := []Node{
			endedAt(plannedAction("done", StateCompleted), time.Minute),
			endedAt(plannedAction("withdrawn", StateAborted), 2*time.Minute),
			endedAt(plannedAction("cut-1", StateExhausted), 3*time.Minute),
			endedAt(plannedAction("cut-2", StateExhausted), 4*time.Minute),
			endedAt(plannedAction("last", last), 5*time.Minute),
		}
		edges := []Edge{}
		for _, id := range []string{"done", "withdrawn", "cut-1", "cut-2", "last"} {
			edges = append(edges, pursuitEdge(id, "lead"))
		}
		// done 报出了新事实：推进了，线索不会被它关闭。
		edges = append(edges, generatedBy("done", observation("fact", "access", "换了个入口"))...)
		return deriveLeads([]Node{factLead("lead"), observation("fact", "access", "换了个入口")}, actions, edges)
	}

	stalled := build(StateExhausted)
	require.Len(t, stalled, 1)
	assert.True(t, stalled[0].Pending())
	assert.Equal(t, 3, stalled[0].Truncated)
	assert.Equal(t, 3, stalled[0].TruncationStreak, "中间被收回的那次不打断连续计数")

	broke := build(StateFailed)
	require.Len(t, broke, 1)
	assert.Equal(t, 2, broke[0].Truncated)
	assert.Zero(t, broke[0].TruncationStreak, "最近一次已了结的跟进不是没做完，连续计数归零")
	assert.Equal(t, 1, broke[0].Progressed)
}

// 多次跟进都得出关闭结论时，原因以先发生的那次为准；跟进按结局的先后结算，不随存储返回
// 边的顺序变化。
func TestDeriveLeads_ClosureKeepsTheEarliestReason(t *testing.T) {
	claim := observation("claim", "hypothesis", "提交后整库被重置")
	// newest 先报出了主张（有结论）；earliest 更早完成却什么都没留下（干涸）。
	actions := []Node{endedAt(plannedAction("newest", StateCompleted), 2*time.Minute), endedAt(plannedAction("earliest", StateCompleted), time.Minute)}
	edges := append(generatedBy("newest", claim),
		pursuitEdge("newest", "lead"), pursuitEdge("earliest", "lead"))

	leads := deriveLeads([]Node{factLead("lead"), claim}, actions, edges)
	require.Len(t, leads, 1)
	require.NotNil(t, leads[0].Closure)
	assert.Equal(t, ClosureDry, leads[0].Closure.Kind, "先发生的那次结局关闭了线索，之后的不改写原因")

	slices.Reverse(edges)
	leads = deriveLeads([]Node{factLead("lead"), claim}, actions, edges)
	require.NotNil(t, leads[0].Closure)
	assert.Equal(t, ClosureDry, leads[0].Closure.Kind, "边的返回顺序不影响结算")
}

// 在办动作跟进的线索不在待跟进清单里，已关闭的单列；跟进完什么都没留下的线索已干涸。
func TestStore_OpenLeads_SplitByStanding(t *testing.T) {
	ctx := context.Background()
	store := revisionFixture(t)
	leads, err := store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	assert.Empty(t, leads, "没有线索的任务没有待跟进的线索")

	seedLead(t, store, "waiting")
	seedLead(t, store, "held")
	seedLead(t, store, "shut")
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act", StateRunning),
		[]Edge{pursuitEdge("act", "held")}))
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("dry", StateCompleted),
		[]Edge{pursuitEdge("dry", "shut")}))

	open, err := store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	require.Len(t, open, 1, "只有没人跟进、没被关闭的线索待深挖")
	assert.Equal(t, "waiting", open[0].ID)

	closed, err := store.ClosedLeads(ctx, revTask)
	require.NoError(t, err)
	require.Len(t, closed, 1)
	assert.Equal(t, "shut", closed[0].ID)
	assert.Equal(t, ClosureDry, closed[0].Closure.Kind, "跟进完什么都没留下：干涸")
}

// PursuedLeads 只返回这个动作认领的线索，别的动作认领的不在其列。
func TestStore_PursuedLeads_OnlyOwnClaims(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seedLead(t, store, "claimed")
	seedLead(t, store, "other")
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act", StateOpen),
		[]Edge{pursuitEdge("act", "claimed")}))
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("other-act", StateOpen),
		[]Edge{pursuitEdge("other-act", "other")}))

	leads, err := store.PursuedLeads(ctx, revTask, "act")
	require.NoError(t, err)
	require.Len(t, leads, 1)
	assert.Equal(t, "claimed", leads[0].ID)
	assert.Equal(t, "探通了一条路", leads[0].Statement())

	none, err := store.PursuedLeads(ctx, revTask, "不存在的动作")
	require.NoError(t, err)
	assert.Empty(t, none)
}

// 认领落 pursues 边：线索由在办动作跟进，退出待深挖清单。
func TestRevisePlan_ClaimCreatesPursuesEdge(t *testing.T) {
	ctx := context.Background()
	store := revisionFixture(t)
	seedLead(t, store, "lead-1")

	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis:        basisOf(t, store),
		AddedActions: []ActionDraft{{Node: plannedAction("act-1", StateOpen), Pursues: []string{"lead-1"}}},
	})
	require.NoError(t, err)
	assert.Empty(t, report.Rejected, "认领一条真实待深挖的线索不被剔除")
	assert.True(t, edgesOf(t, store, RelPursues)[[2]string{"act-1", "lead-1"}], "落一条 act→lead 的 pursues 边")

	leads, err := store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	assert.Empty(t, leads, "线索由在办动作跟进")
}

func TestRevisePlan_PursuitRejectsStaleReferences(t *testing.T) {
	ctx := context.Background()
	store := revisionFixture(t)
	seedLead(t, store, "busy")
	seedLead(t, store, "shut")
	seedLead(t, store, "once")
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act-prev", StateOpen), []Edge{pursuitEdge("act-prev", "busy")}))

	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis: basisOf(t, store),
		AddedActions: []ActionDraft{
			{Node: plannedAction("act-2", StateOpen), Pursues: []string{"busy", "shut", "幻觉ID", "once"}},
			{Node: plannedAction("act-3", StateOpen), Pursues: []string{"once"}},
		},
		ClosedLeads: []LeadClosing{{LeadID: "shut", Kind: ClosureNotWorth, Reason: "价值够不上一个动作"}},
	})
	require.NoError(t, err)
	require.Len(t, report.AddedActions, 2, "动作照常落图，不被无效引用拖垮")
	assert.Equal(t, []string{"shut"}, report.ClosedLeads, "关闭成立")

	want := map[string]string{
		"busy": "不是本任务待深挖的线索", "shut": "同一修订已关闭这条线索",
		"幻觉ID": "不是本任务待深挖的线索", "once": "同一修订里已有动作认领这条线索",
	}
	require.Len(t, report.Rejected, len(want))
	for _, r := range report.Rejected {
		assert.Equal(t, FieldPursues, r.Field)
		assert.Equal(t, want[r.ID], r.Reason, r.ID)
	}
	pursues := edgesOf(t, store, RelPursues)
	assert.False(t, pursues[[2]string{"act-2", "busy"}])
	assert.False(t, pursues[[2]string{"act-2", "shut"}], "已关闭的线索不能同时被跟进")
	assert.True(t, pursues[[2]string{"act-2", "once"}], "先到的那次跟进成立")
	assert.False(t, pursues[[2]string{"act-3", "once"}], "一条线索在一次修订里只有一个动作跟进")
}

// 关闭线索：类别与理由批注在线索上，它退出待跟进、进入已关闭；线索信号与节点来源不受
// 影响。同一条线索在一次修订里出现两次只关一次。
func TestRevisePlan_ClosesLead(t *testing.T) {
	ctx := context.Background()
	store := revisionFixture(t)
	seedLead(t, store, "lead-1")
	seedLead(t, store, "lead-2")

	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis: basisOf(t, store),
		ClosedLeads: []LeadClosing{
			{LeadID: "lead-1", Kind: ClosureOutOfScope, Reason: "  超出任务目标  "},
			{LeadID: "lead-1", Kind: ClosureBlocked, Reason: "重复出现"},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, report.Rejected)
	assert.Equal(t, []string{"lead-1"}, report.ClosedLeads)

	lead, err := store.GetNode(ctx, "lead-1")
	require.NoError(t, err)
	closure, ok := lead.LeadClosure()
	require.True(t, ok)
	assert.Equal(t, LeadClosure{Kind: ClosureOutOfScope, Reason: "超出任务目标"}, closure, "类别与理由落在线索上（理由去首尾空白）")
	assert.True(t, lead.OpensDeeper(), "关闭不抹掉线索信号")
	assert.Equal(t, SourceExecutor, lead.SourceType, "关闭不触碰节点来源")

	open, err := store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	require.Len(t, open, 1, "另一条线索仍待深挖")
	assert.Equal(t, "lead-2", open[0].ID)
}

// 关闭的引用失效（不是待深挖的线索）、类别不是规划者可判定的、或没给理由：剔除该条并
// 报告，不在线索上留批注，线索照常待深挖。
func TestRevisePlan_ClosingRejectsStaleAndMalformed(t *testing.T) {
	ctx := context.Background()
	store := revisionFixture(t)
	seedLead(t, store, "busy")
	seedLead(t, store, "reasonless")
	seedLead(t, store, "derived-kind")
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act-prev", StateOpen), []Edge{pursuitEdge("act-prev", "busy")}))

	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis: basisOf(t, store),
		ClosedLeads: []LeadClosing{
			{LeadID: "busy", Kind: ClosureNotWorth, Reason: "已被在办动作跟进"},
			{LeadID: "reasonless", Kind: ClosureNotWorth, Reason: "   "},
			{LeadID: "derived-kind", Kind: ClosureDry, Reason: "想直接写推导出来的类别"},
			{LeadID: "幻觉ID", Kind: ClosureNotWorth, Reason: "x"},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, report.ClosedLeads)
	require.Len(t, report.Rejected, 4)
	for _, r := range report.Rejected {
		assert.Equal(t, FieldLeads, r.Field)
	}
	lead, err := store.GetNode(ctx, "reasonless")
	require.NoError(t, err)
	_, ok := lead.LeadClosure()
	assert.False(t, ok, "没给理由的不批注")

	open, err := store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	require.Len(t, open, 2, "剔除的条目不影响线索的处境")
	assert.Equal(t, "reasonless", open[0].ID)
	assert.Equal(t, "derived-kind", open[1].ID)
}

// 既不跟进也不关闭线索的修订不加载线索快照：不碰线索的常规规划零额外开销。
func TestRevisePlan_WithoutLeadReferencesAddsActions(t *testing.T) {
	store := revisionFixture(t)
	report, err := store.RevisePlan(context.Background(), revTask, PlanRevision{
		Basis:        basisOf(t, store),
		AddedActions: []ActionDraft{{Node: plannedAction("act-3", StateOpen)}},
	})
	require.NoError(t, err)
	assert.Empty(t, report.Rejected)
	require.Len(t, report.AddedActions, 1)
}
