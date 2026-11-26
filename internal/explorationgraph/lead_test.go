package explorationgraph

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/framework/core"
)

// factLead 建一条标了 opens_deeper 的事实观察（无验证状态=事实）。
func factLead(id string) Node {
	return Node{
		ID: id, TaskID: revTask, Kind: core.KindObservation, SourceType: SourceExecutor,
		Content: []byte(`{"category":"access","statement":"探通了一条路"}`), Metadata: OpensDeeperMetadata(),
	}
}

// pursueEdge 建一条 action→lead 的 pursues 边。
func pursueEdge(actionID, leadID string) Edge {
	return Edge{TaskID: revTask, SrcID: actionID, Rel: RelPursues, DstID: leadID}
}

// plannedAction 建一个规划者排出的、处在给定状态的动作。
func plannedAction(id string, state State) Node {
	return Node{
		ID: id, TaskID: revTask, Kind: core.KindAction, State: stateOf(state),
		SourceType: SourcePlanner, Content: []byte(`{"instruction":"深挖"}`),
	}
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

// 不追的理由从批注里读；没有批注、批注损坏都按「没有被判不追」处理，不 panic。
func TestNode_LeadDeclineReason(t *testing.T) {
	lead := factLead("l1")
	assert.Empty(t, lead.LeadDeclineReason(), "没有批注")

	lead.Metadata, _ = json.Marshal(map[string]interface{}{
		"opens_deeper": true, "lead_declined": map[string]interface{}{"reason": "超出任务目标"},
	})
	assert.Equal(t, "超出任务目标", lead.LeadDeclineReason())

	lead.Metadata = json.RawMessage(`{"lead_declined":"损坏的批注"}`)
	assert.Empty(t, lead.LeadDeclineReason(), "批注形状不对")

	lead.Metadata = json.RawMessage(`not json`)
	assert.Empty(t, lead.LeadDeclineReason(), "metadata 解析失败")
}

// 线索是否待深挖由图结构推导：没有算数的跟进动作就是待深挖。在办（open/running）与
// 已有结局（completed/failed）的跟进都算数；半途而废的（撤回、否决、迭代预算耗尽）
// 不算——线索回到待深挖。这是「跟进没做完，线索不该消失」的核心。
func TestStore_OpenLeads_DerivedFromPursuit(t *testing.T) {
	cases := []struct {
		name   string
		state  *State
		isOpen bool
	}{
		{"没有动作跟进", nil, true},
		{"open 跟进", stateOf(StateOpen), false},
		{"running 跟进", stateOf(StateRunning), false},
		{"completed 跟进：已办结", stateOf(StateCompleted), false},
		{"failed 跟进：试过且出错", stateOf(StateFailed), false},
		{"exhausted 跟进：预算耗尽、没推进到结论，回到待深挖", stateOf(StateExhausted), true},
		{"aborted 跟进：撤回，回到待深挖", stateOf(StateAborted), true},
		{"rejected 跟进：否决，回到待深挖", stateOf(StateRejected), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			store := NewMemoryStore()
			seedNode(t, store, factLead("lead"))
			if c.state != nil {
				require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act", *c.state), []Edge{pursueEdge("act", "lead")}))
			}
			leads, err := store.OpenLeads(ctx, revTask)
			require.NoError(t, err)
			if c.isOpen {
				require.Len(t, leads, 1, "线索仍待深挖")
				assert.Equal(t, "lead", leads[0].ID)
			} else {
				assert.Empty(t, leads, "线索不在待深挖清单里")
			}
		})
	}
}

// 跟进动作被收回（规划者停止探索）后线索回到待深挖，再次被认领即再次退出：推导随时按
// 当前图面重算，不靠一次性水位。
func TestStore_OpenLeads_ReclaimedAfterWithdrawal(t *testing.T) {
	ctx := context.Background()
	store := revisionFixture(t)
	seedNode(t, store, factLead("lead"))
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act-1", StateOpen), []Edge{pursueEdge("act-1", "lead")}))

	withdrawn, err := store.StopExploration(ctx, revTask, basisOf(t, store), "测试：收回跟进动作")
	require.NoError(t, err)
	require.Contains(t, withdrawn, "act-1", "跟进动作被收回")

	leads, err := store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	require.Len(t, leads, 1, "撤回后线索回到待深挖")

	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act-2", StateRunning), []Edge{pursueEdge("act-2", "lead")}))
	leads, err = store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	assert.Empty(t, leads, "再次被认领即再次退出")
}

// PursuedLeads 返回动作经 pursues 边跟进的全部线索；没有跟进的动作得到空列表。
func TestStore_PursuedLeads(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	seedNode(t, store, factLead("lead-1"))
	seedNode(t, store, factLead("lead-2"))
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act", StateOpen),
		[]Edge{pursueEdge("act", "lead-1"), pursueEdge("act", "lead-2")}))

	leads, err := store.PursuedLeads(ctx, revTask, "act")
	require.NoError(t, err)
	require.Len(t, leads, 2)
	assert.Equal(t, "lead-1", leads[0].ID)
	assert.Equal(t, "lead-2", leads[1].ID)

	none, err := store.PursuedLeads(ctx, revTask, "不存在的动作")
	require.NoError(t, err)
	assert.Empty(t, none)
}

// RevisePlan 认领线索：落一条 pursues 边（action → 线索），线索被跟进后退出待深挖清单。
func TestRevisePlan_PursuesLeadCreatesEdge(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	seedNode(t, store, factLead("lead-1"))

	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis:        basisOf(t, store),
		AddedActions: []ActionDraft{{Node: plannedAction("act-1", StateOpen), PursuesLead: "lead-1"}},
	})
	require.NoError(t, err)
	assert.Empty(t, report.Rejected, "认领真实待深挖线索不被剔除")
	assert.True(t, edgesOf(t, store, RelPursues)[[2]string{"act-1", "lead-1"}], "落一条 act→lead 的 pursues 边")

	leads, err := store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	assert.Empty(t, leads, "线索被跟进，退出待深挖清单")
}

// 认领不存在 / 已被在办动作跟进的线索：剔除该引用并如实报告，不拖垮修订（动作照常落图，
// 也不落无效的 pursues 边）。
func TestRevisePlan_PursuesLeadRejectsStaleReference(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	// 线索已被一个 open 动作跟进 → 不再是待深挖线索。
	seedNode(t, store, factLead("busy"))
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act-prev", StateOpen), []Edge{pursueEdge("act-prev", "busy")}))

	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis:        basisOf(t, store),
		AddedActions: []ActionDraft{{Node: plannedAction("act-2", StateOpen), PursuesLead: "busy"}},
	})
	require.NoError(t, err)
	require.Len(t, report.Rejected, 1, "已被跟进的线索引用被剔除")
	assert.Equal(t, FieldPursuesLead, report.Rejected[0].Field)
	assert.Equal(t, "busy", report.Rejected[0].ID)
	require.Len(t, report.AddedActions, 1, "动作照常落图，不被无效引用拖垮")
	assert.False(t, edgesOf(t, store, RelPursues)[[2]string{"act-2", "busy"}], "无效认领不落 pursues 边")
}

// RevisePlan 判定不追：理由批注在线索上，线索退出待深挖、进入已不追；线索信号与节点
// 来源不受影响。同一条线索在一次修订里出现两次只判一次。
func TestRevisePlan_DeclineLead(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	seedNode(t, store, factLead("lead-1"))
	seedNode(t, store, factLead("lead-2"))

	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis: basisOf(t, store),
		DeclinedLeads: []LeadDecline{
			{LeadID: "lead-1", Reason: "  超出任务目标  "},
			{LeadID: "lead-1", Reason: "重复出现"},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, report.Rejected)
	assert.Equal(t, []string{"lead-1"}, report.DeclinedLeads)

	lead, err := store.GetNode(ctx, "lead-1")
	require.NoError(t, err)
	assert.Equal(t, "超出任务目标", lead.LeadDeclineReason(), "理由落在线索上（去首尾空白）")
	assert.True(t, lead.OpensDeeper(), "不追不抹掉线索信号")
	assert.Equal(t, SourceExecutor, lead.SourceType, "不追不触碰节点来源")

	open, err := store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	require.Len(t, open, 1, "另一条线索仍待深挖")
	assert.Equal(t, "lead-2", open[0].ID)

	declined, err := store.DeclinedLeads(ctx, revTask)
	require.NoError(t, err)
	require.Len(t, declined, 1)
	assert.Equal(t, "lead-1", declined[0].ID)
}

// 判定不追的引用失效（不是待深挖线索）或没给理由：剔除该条并报告，不在线索上留批注。
func TestRevisePlan_DeclineLeadRejectsStaleAndReasonless(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	seedNode(t, store, factLead("busy"))
	require.NoError(t, store.CreateNodeWithEdges(ctx, plannedAction("act-prev", StateOpen), []Edge{pursueEdge("act-prev", "busy")}))
	seedNode(t, store, factLead("reasonless"))

	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis: basisOf(t, store),
		DeclinedLeads: []LeadDecline{
			{LeadID: "busy", Reason: "已被在办动作跟进"},
			{LeadID: "reasonless", Reason: "   "},
			{LeadID: "幻觉ID", Reason: "x"},
		},
	})
	require.NoError(t, err)
	assert.Empty(t, report.DeclinedLeads)
	require.Len(t, report.Rejected, 3)
	for _, r := range report.Rejected {
		assert.Equal(t, FieldDeclinedLeads, r.Field)
	}

	lead, err := store.GetNode(ctx, "reasonless")
	require.NoError(t, err)
	assert.Empty(t, lead.LeadDeclineReason(), "没给理由的不批注")

	open, err := store.OpenLeads(ctx, revTask)
	require.NoError(t, err)
	require.Len(t, open, 1, "没给理由的线索仍待深挖")
	assert.Equal(t, "reasonless", open[0].ID)
}

// 同一条线索在同一次修订里既被判不追又被认领：不追成立，认领落空并被报告——两种处置
// 互斥，不能既不追又有动作在跟进它。
func TestRevisePlan_DeclinedLeadCannotBePursuedInSameRevision(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	seedNode(t, store, factLead("lead-1"))

	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis:         basisOf(t, store),
		AddedActions:  []ActionDraft{{Node: plannedAction("act-1", StateOpen), PursuesLead: "lead-1"}},
		DeclinedLeads: []LeadDecline{{LeadID: "lead-1", Reason: "前提已被证伪"}},
	})
	require.NoError(t, err)
	require.Len(t, report.Rejected, 1)
	assert.Equal(t, FieldPursuesLead, report.Rejected[0].Field)
	assert.Equal(t, "lead-1", report.Rejected[0].ID)
	require.Len(t, report.AddedActions, 1, "动作照常落图")
	assert.Empty(t, edgesOf(t, store, RelPursues), "不落 pursues 边")
	assert.Equal(t, []string{"lead-1"}, report.DeclinedLeads, "不追成立")
}

// 既不认领也不判定线索的修订不加载线索（openLeadIndex 跳过）：不碰线索的常规规划零额外开销。
func TestRevisePlan_WithoutLeadReferencesAddsActions(t *testing.T) {
	store := revisionFixture(t)
	ctx := context.Background()
	report, err := store.RevisePlan(ctx, revTask, PlanRevision{
		Basis:        basisOf(t, store),
		AddedActions: []ActionDraft{{Node: plannedAction("act-3", StateOpen)}},
	})
	require.NoError(t, err)
	assert.Empty(t, report.Rejected)
	require.Len(t, report.AddedActions, 1)
}
