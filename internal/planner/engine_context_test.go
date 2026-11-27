package planner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/evaluator"
	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// contextObservation 构造一条观察：status 非空即假设（证伪时带原因），为空即事实。
func contextObservation(id, category, statement string, status *explorationgraph.VerificationStatus, refuteReason string, offset time.Duration) explorationgraph.Node {
	content, _ := json.Marshal(map[string]string{"category": category, "statement": statement})
	n := explorationgraph.Node{
		ID: id, TaskID: revisionTask, Kind: core.KindObservation, Content: content,
		VerificationStatus: status, SourceType: explorationgraph.SourceExecutor,
		CreatedAt: time.Unix(1700000000, 0).Add(offset),
	}
	if refuteReason != "" {
		n.Metadata, _ = json.Marshal(map[string]string{"refute_reason": refuteReason})
	}
	return n
}

func verification(s explorationgraph.VerificationStatus) *explorationgraph.VerificationStatus {
	return &s
}

// 已证伪的假设无论因何出圈（评估者证伪、机器护栏、验证重试耗尽、评估者不可用）都进规划
// 视野，并带上原因：规划器据原因决定换配方、原方向重提还是放弃。待验证与已坐实的假设不在其列。
func TestGatherContext_RefutedHypothesesCarryEveryReason(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	refuted := verification(explorationgraph.VerificationRefuted)
	reasons := []string{
		evaluator.RefuteClaimInvalid, evaluator.RefuteNotReproducible, evaluator.RefuteAssertNotDiscriminative,
		evaluator.RefuteInvalidRecipe, evaluator.RefuteJudgeUnavailable,
	}
	nodes := []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		contextObservation("pending", "hypothesis", "待验证", verification(explorationgraph.VerificationPending), "", time.Second),
		contextObservation("confirmed", "hypothesis", "已坐实", verification(explorationgraph.VerificationConfirmed), "", time.Second),
	}
	for i, r := range reasons {
		nodes = append(nodes, contextObservation("h-"+r, "hypothesis", "假设-"+r, refuted, r, time.Duration(i+2)*time.Second))
	}
	for _, n := range nodes {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}

	planCtx, err := (&Engine{}).gatherContext(context.Background(), store, revisionTask)
	require.NoError(t, err)

	got := map[string]string{}
	for _, h := range planCtx.RefutedHypotheses {
		got[h.Statement] = h.Reason
	}
	want := map[string]string{}
	for _, r := range reasons {
		want["假设-"+r] = r
	}
	assert.Equal(t, want, got)
	assert.Empty(t, planCtx.Facts, "假设不是事实")
}

// 评估者的证伪理由随假设进规划视野：原因只是类别，缺了什么证据、哪部分已被证明要看理由。
func TestGatherContext_RefutedHypothesisCarriesDetail(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	h := contextObservation("h", "hypothesis", "接口可读取任意记录", verification(explorationgraph.VerificationRefuted), "", time.Second)
	h.Metadata, _ = json.Marshal(map[string]string{"refute_reason": evaluator.RefuteNotReproducible, "refute_detail": "只读到本人记录"})
	for _, n := range []explorationgraph.Node{revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0), h} {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}

	planCtx, err := (&Engine{}).gatherContext(context.Background(), store, revisionTask)
	require.NoError(t, err)
	require.Len(t, planCtx.RefutedHypotheses, 1)
	assert.Equal(t, "只读到本人记录", planCtx.RefutedHypotheses[0].Detail)
}

// 待验证的假设进规划视野：规划者看不见它们时，会为已上报、正在复现门里排队的主张再排动作
// 重新取得（实测：flag 待验证期间被重复取得、重复上报，多出整批动作）。
func TestGatherContext_PendingHypothesesVisible(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	nodes := []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		contextObservation("pending", "hypothesis", "接口泄露配置", verification(explorationgraph.VerificationPending), "", time.Second),
		contextObservation("confirmed", "hypothesis", "已坐实", verification(explorationgraph.VerificationConfirmed), "", 2*time.Second),
		contextObservation("refuted", "hypothesis", "已证伪", verification(explorationgraph.VerificationRefuted), evaluator.RefuteClaimInvalid, 3*time.Second),
	}
	for _, n := range nodes {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}

	planCtx, err := (&Engine{}).gatherContext(context.Background(), store, revisionTask)
	require.NoError(t, err)
	assert.Equal(t, []string{"接口泄露配置"}, planCtx.PendingHypotheses)
}

// 事实按类别分组呈现给规划器（有什么 / 怎么进入 / 怎么连接 / 怎么反应 / 其他），同类别内
// 近者在前；规划器据此先看清环境的各个侧面，而不是读一串混杂的陈述。
func TestGatherContext_FactsGroupedByCategory(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	for _, n := range []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		contextObservation("f1", "behavior", "搜索参数原样回显", nil, "", 1*time.Second),
		contextObservation("f2", "entity", "目标为 nginx 1.18", nil, "", 2*time.Second),
		contextObservation("f3", "note", "执行总结：完成", nil, "", 3*time.Second),
		contextObservation("f4", "access", "登录接口 /login 用表单认证", nil, "", 4*time.Second),
		contextObservation("f5", "entity", "存在 /admin 页面", nil, "", 5*time.Second),
	} {
		_, err := store.CreateNode(context.Background(), n)
		require.NoError(t, err)
	}

	planCtx, err := (&Engine{}).gatherContext(context.Background(), store, revisionTask)
	require.NoError(t, err)
	section := buildFactsSection(planCtx.Facts)

	order := []string{"存在 /admin 页面", "目标为 nginx 1.18", "登录接口 /login 用表单认证", "搜索参数原样回显", "执行总结：完成"}
	last := -1
	for _, s := range order {
		i := strings.Index(section, s)
		require.GreaterOrEqual(t, i, 0, "缺少事实 %q：\n%s", s, section)
		assert.Greater(t, i, last, "事实 %q 次序不对：\n%s", s, section)
		last = i
	}
	for _, heading := range []string{"有什么", "怎么进入", "怎么反应", "其他"} {
		assert.Contains(t, section, heading)
	}
	assert.NotContains(t, section, "怎么连接", "没有该类别的事实就不出现该类别标题")
}

// 线索进规划视野：开着、没有在办动作跟进的线索待深挖（带短号排在前）；已关闭的带着原因
// 单列。线索仍是事实，照常进 Facts。
func TestGatherContext_Leads(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	ctx := context.Background()
	openLead := contextObservation("lead-open", "access", "管理入口未鉴权可达", nil, "", 1*time.Second)
	openLead.Metadata = explorationgraph.OpensDeeperMetadata()
	held := contextObservation("lead-held", "behavior", "参数回显了输入", nil, "", 2*time.Second)
	held.Metadata = explorationgraph.OpensDeeperMetadata()
	shut := contextObservation("lead-shut", "entity", "旧版本提示", nil, "", 3*time.Second)
	shut.Metadata, _ = json.Marshal(map[string]interface{}{
		"opens_deeper": true, "lead_closed": map[string]interface{}{"kind": "out_of_scope", "reason": "超出任务目标"},
	})
	plain := contextObservation("plain", "entity", "目标为 nginx", nil, "", 4*time.Second)

	for _, n := range []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		openLead, held, shut, plain,
	} {
		_, err := store.CreateNode(ctx, n)
		require.NoError(t, err)
	}
	pursuing := revisionNode("act-1", core.KindAction, explorationgraph.SourcePlanner, openState(), 5*time.Second)
	require.NoError(t, store.CreateNodeWithEdges(ctx, pursuing, []explorationgraph.Edge{
		{TaskID: revisionTask, SrcID: "act-1", Rel: explorationgraph.RelPursues, DstID: "lead-held"},
	}))

	planCtx, err := (&Engine{}).gatherContext(ctx, store, revisionTask)
	require.NoError(t, err)

	require.Len(t, planCtx.OpenLeads, 1, "只有没被跟进、没被关闭的线索待深挖")
	assert.Equal(t, OpenLead{ID: "lead-open", Statement: "管理入口未鉴权可达", CreatedAt: openLead.CreatedAt}, planCtx.OpenLeads[0])

	require.Equal(t, []ClosedLead{{Statement: "旧版本提示", Kind: explorationgraph.ClosureOutOfScope, Reason: "超出任务目标"}}, planCtx.ClosedLeads)

	// 线索仍是事实，四条都进 Facts（线索是事实的一个侧面，不替代事实基底）
	statements := map[string]bool{}
	for _, f := range planCtx.Facts {
		statements[f.Statement] = true
	}
	for _, want := range []string{"管理入口未鉴权可达", "参数回显了输入", "旧版本提示", "目标为 nginx"} {
		assert.True(t, statements[want], "事实 %q 应仍在 Facts 里", want)
	}
}

// 线索的处境与归属都从图上带进规划视野：所属方向沿来源链推导、产出它的动作是否在办、几次
// 跟进而没做完；进行中的方向带短号列出，在办动作记下它推进的方向与是否在跟进线索。
func TestGatherContext_LeadStandingAndAttribution(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	ctx := context.Background()
	chained := planFactLead("chained", time.Second)
	cut := planFactLead("cut", 2*time.Second)
	for _, n := range []explorationgraph.Node{
		revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0),
		revisionNode("d1", core.KindObjective, explorationgraph.SourcePlanner, openState(), time.Second),
		revisionNode("d-done", core.KindObjective, explorationgraph.SourcePlanner, statePointer(explorationgraph.StateCompleted), time.Second),
		chained, cut,
	} {
		_, err := store.CreateNode(ctx, n)
		require.NoError(t, err)
	}
	running, exhausted := explorationgraph.StateRunning, explorationgraph.StateExhausted
	// producer 在办、推进 d1，产出了线索 chained。
	require.NoError(t, store.CreateNodeWithEdges(ctx, revisionNode("producer", core.KindAction, explorationgraph.SourcePlanner, &running, 3*time.Second),
		[]explorationgraph.Edge{
			{TaskID: revisionTask, SrcID: "producer", Rel: explorationgraph.RelGenerates, DstID: "chained"},
			{TaskID: revisionTask, SrcID: "producer", Rel: explorationgraph.RelServes, DstID: "d1"},
		}))
	// cutter 跟进线索 cut，迭代预算耗尽而没做完。
	require.NoError(t, store.CreateNodeWithEdges(ctx, revisionNode("cutter", core.KindAction, explorationgraph.SourcePlanner, &exhausted, 4*time.Second),
		[]explorationgraph.Edge{{TaskID: revisionTask, SrcID: "cutter", Rel: explorationgraph.RelPursues, DstID: "cut"}}))

	planCtx, err := (&Engine{}).gatherContext(ctx, store, revisionTask)
	require.NoError(t, err)

	assert.Equal(t, []Direction{{ID: "d1", Goal: "d1"}}, planCtx.Directions, "只列进行中的方向")
	require.Len(t, planCtx.OpenLeads, 2)
	byID := map[string]OpenLead{}
	for _, lead := range planCtx.OpenLeads {
		byID[lead.ID] = lead
	}
	assert.Equal(t, "d1", byID["chained"].DirectionID, "线索的方向取产出它的动作的方向")
	assert.Equal(t, "producer", byID["chained"].ProducerID)
	assert.Equal(t, 1, byID["cut"].Truncated, "没做完的次数带进规划视野")
	assert.Equal(t, 1, byID["cut"].TruncationStreak)
	assert.Empty(t, byID["cut"].DirectionID)

	assert.Equal(t, "d1", planCtx.actionDirection["producer"])
	assert.False(t, planCtx.claiming["producer"], "产出线索不等于跟进线索")
	assert.True(t, planCtx.claiming["cutter"])
}

func statePointer(s explorationgraph.State) *explorationgraph.State { return &s }
