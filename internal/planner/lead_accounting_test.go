package planner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
	"github.com/V3teran/liusha/internal/framework/llm"
	"github.com/V3teran/liusha/internal/framework/runtime"
)

// leadAccountingContext 建一份规划上下文：四条待深挖线索（L1 与 L3 是同一陈述，在输入里并成
// 一行）。
func leadAccountingContext() *PlanningContext {
	return &PlanningContext{
		OpenLeads: []OpenLead{
			{ID: "lead-1", Statement: "甲"},
			{ID: "lead-2", Statement: "乙"},
			{ID: "lead-3", Statement: "甲"},
			{ID: "lead-4", Statement: "丙"},
		},
	}
}

// 短号是清单里的序号：同一陈述的线索引用其中任意一个短号都指这一行上的全部线索；越界、
// 前缀不对、不是数字的都不认。
func TestPlanningContext_ResolvesHandles(t *testing.T) {
	ctx := leadAccountingContext()

	ids, ok := ctx.leadIDs("L3")
	require.True(t, ok)
	assert.Equal(t, []string{"lead-1", "lead-3"}, ids, "同一陈述的线索一起返回")
	for _, bad := range []string{"L5", "L0", "Lx", "D1", ""} {
		_, ok := ctx.leadIDs(bad)
		assert.False(t, ok, "不认 %q", bad)
	}
}

// 每条待深挖的线索恰交代一次：被本轮动作的 pursues 引用（认领），或在 leads 里有处置。全部
// 成立时没有问题，解析结果直接可落图。
func TestAccountLeads_EveryLeadAccountedOnce(t *testing.T) {
	plan := accountLeads(leadAccountingContext(), &PlanningResponse{
		Actions: []ActionProposal{
			{Instruction: "沿甲深挖", Pursues: []string{"L1"}},
			{Instruction: "沿乙深挖", Pursues: []string{"L2"}},
		},
		Leads: []LeadDecisionProposal{
			{ID: "L4", Decision: "close", Kind: "blocked", Reason: "需要管理员凭据"},
		},
	})

	assert.Empty(t, plan.problems)
	assert.Equal(t, map[int][]string{0: {"lead-1", "lead-3"}, 1: {"lead-2"}}, plan.claims, "引用 L1 即认领这一行上的两条线索")
	assert.Equal(t, []explorationgraph.LeadClosing{{LeadID: "lead-4", Kind: explorationgraph.ClosureBlocked, Reason: "需要管理员凭据"}}, plan.closings)
	assert.Zero(t, plan.left)
}

// 留待只计数、不产生任何落图的内容；处置的大小写与空白不敏感。
func TestAccountLeads_LeaveOnlyCounts(t *testing.T) {
	plan := accountLeads(leadAccountingContext(), &PlanningResponse{
		Leads: []LeadDecisionProposal{
			{ID: "L1", Decision: "LEAVE"},
			{ID: "L2", Decision: " leave "},
		},
	})
	require.Len(t, plan.problems, 1, "丙没人交代")
	assert.Equal(t, "L4：还没有交代", plan.problems[0].handle+"："+plan.problems[0].message)
	assert.Equal(t, 3, plan.left, "同一行的两条线索都算留待")
	assert.Empty(t, plan.claims)
	assert.Empty(t, plan.closings)
}

// 输出里一条都没提到的线索按输入里的行报出来（同一行的短号一起列）；提到了但交代不成立的
// 只报那一条，不再算作没有交代。
func TestAccountLeads_ReportsLeadsNobodyMentioned(t *testing.T) {
	plan := accountLeads(leadAccountingContext(), &PlanningResponse{})
	require.Len(t, plan.problems, 3)
	assert.Equal(t, []string{"L1、L3：还没有交代", "L2：还没有交代", "L4：还没有交代"}, plan.describe())

	plan = accountLeads(leadAccountingContext(), &PlanningResponse{
		Leads: []LeadDecisionProposal{{ID: "L3", Decision: "close", Kind: "dry", Reason: "想写推导出来的类别"}},
	})
	assert.Equal(t, []string{"L3：close 的 kind 不在契约列出的类别里", "L2：还没有交代", "L4：还没有交代"}, plan.describe(),
		"L3 所在的行提到了，只报交代不成立的那一条；别的行照报没有交代")
}

// 交代不成立的原样列出，成立的部分不受影响：引用了不存在的短号、同一条线索交代两次、
// 关闭的类别不对或没写理由、decision 不在契约里。
func TestAccountLeads_ReportsInvalidDispositions(t *testing.T) {
	plan := accountLeads(leadAccountingContext(), &PlanningResponse{
		Actions: []ActionProposal{{Instruction: "沿乙深挖", Pursues: []string{"L2", "L9"}}},
		Leads: []LeadDecisionProposal{
			{ID: "L2", Decision: "leave"},
			{ID: "L4", Decision: "cover"},
			{ID: "L0", Decision: "leave"},
		},
	})
	assert.Equal(t, []string{
		"L9：不是「待深挖的线索」里的编号",
		"L2：被重复交代：一条线索本轮只由一个动作跟进、只交代一次——多出的认领去掉 pursues",
		"L4：decision 只能是 close 或 leave",
		"L0：不是「待深挖的线索」里的编号",
		"L1、L3：还没有交代",
	}, plan.describe())
	assert.Equal(t, map[int][]string{0: {"lead-2"}}, plan.claims, "成立的那一次认领保留")
	assert.Zero(t, plan.left, "被重复交代的那条不算留待")

	plan = accountLeads(leadAccountingContext(), &PlanningResponse{Leads: []LeadDecisionProposal{
		{ID: "L1", Decision: "close", Kind: "made_up", Reason: "x"},
		{ID: "L2", Decision: "close", Kind: "not_worth", Reason: "  "},
		{ID: "L4", Decision: "park"},
	}})
	assert.Equal(t, []string{
		"L1：close 的 kind 不在契约列出的类别里",
		"L2：close 缺少 reason",
		"L4：decision 只能是 close 或 leave",
	}, plan.describe())
	assert.Empty(t, plan.closings)
}

// 交代不成立的条目不算已交代：同一条线索后面跟着的成立版本照常生效，不被判成重复。
func TestAccountLeads_InvalidEntryDoesNotCoverTheLead(t *testing.T) {
	plan := accountLeads(leadAccountingContext(), &PlanningResponse{Leads: []LeadDecisionProposal{
		{ID: "L4", Decision: "cover"},
		{ID: "L4", Decision: "close", Kind: "out_of_scope", Reason: " 范围外 "},
		{ID: "L2", Decision: "park"},
		{ID: "L2", Decision: "leave"},
	}})
	assert.Equal(t, []string{
		"L4：decision 只能是 close 或 leave",
		"L2：decision 只能是 close 或 leave",
		"L1、L3：还没有交代",
	}, plan.describe())
	require.Len(t, plan.closings, 1, "后面的成立版本生效")
	assert.Equal(t, "范围外", plan.closings[0].Reason, "理由去首尾空白")
	assert.Equal(t, 1, plan.left)
}

// 规划进行期间图上出现的新线索补进本轮清单：短号接在末尾，原有的不变（模型上一版输出里
// 的引用照旧成立），并记为「规划开始之后才出现」——交代不要求它们；已经在清单里的不重复。
func TestRefreshRound_AppendsWhatAppearedDuringPlanning(t *testing.T) {
	store := explorationgraph.NewMemoryStore()
	ctx := context.Background()
	_, err := store.CreateNode(ctx, revisionNode("root", core.KindObjective, explorationgraph.SourceUser, openState(), 0))
	require.NoError(t, err)
	planCtx, err := (&Engine{}).gatherContext(ctx, store, revisionTask)
	require.NoError(t, err)
	require.Empty(t, planCtx.OpenLeads)

	// 开轮之后：执行者报出一条线索，同一条线索的陈述又出现一次。
	for _, lead := range []explorationgraph.Node{planFactLead("late-lead", time.Second), planFactLead("late-twin", 2*time.Second)} {
		_, err := store.CreateNode(ctx, lead)
		require.NoError(t, err)
	}

	added := followUpEngine().refreshRound(ctx, store, revisionTask, planCtx)
	require.Len(t, added, 2)
	require.Len(t, planCtx.OpenLeads, 2)
	assert.Equal(t, []string{"L1", "L2"}, []string{ordinal(leadPrefix, 0), ordinal(leadPrefix, 1)})
	_, ok := planCtx.leadIDs("L1")
	require.True(t, ok, "新线索可以用短号引用")
	assert.Equal(t, map[string]bool{"late-lead": true, "late-twin": true}, planCtx.lateLeads, "补进来的记为规划开始之后才出现")

	assert.Empty(t, followUpEngine().refreshRound(ctx, store, revisionTask, planCtx), "已经在清单里的不重复补")
	assert.Len(t, planCtx.lateLeads, 2)
}

// 规划开始之后才出现的线索不要求交代：模型这一轮没有见过它们，没提到就是留待下一轮（落图之后
// 只要没有待执行的动作，10 秒内就有下一轮）。提到了但交代不成立的照常报出——引用得出的短号
// 就是给模型认领用的。开轮时就在清单里的线索，没交代照旧报。
func TestAccountLeads_LeadsAppearedDuringPlanningAreNotRequired(t *testing.T) {
	planCtx := leadAccountingContext()
	planCtx.OpenLeads = append(planCtx.OpenLeads, OpenLead{ID: "lead-9", Statement: "丁"})
	planCtx.lateLeads = map[string]bool{"lead-9": true}

	plan := accountLeads(planCtx, &PlanningResponse{})
	assert.Equal(t, []string{"L1、L3：还没有交代", "L2：还没有交代", "L4：还没有交代"}, plan.describe(), "规划期间出现的 L5 不算没有交代")

	plan = accountLeads(planCtx, &PlanningResponse{
		Actions: []ActionProposal{{Instruction: "沿丁深挖", Pursues: []string{"L5"}}},
		Leads:   []LeadDecisionProposal{{ID: "L4", Decision: "leave"}},
	})
	assert.Equal(t, []string{"L1、L3：还没有交代", "L2：还没有交代"}, plan.describe())
	assert.Equal(t, []string{"lead-9"}, plan.claims[0], "模型经 graph_read 看到并认领了它，引用成立")

	plan = accountLeads(planCtx, &PlanningResponse{
		Leads: []LeadDecisionProposal{{ID: "L5", Decision: "close", Kind: "dry", Reason: "想写推导出来的类别"}},
	})
	assert.Equal(t, []string{"L5：close 的 kind 不在契约列出的类别里", "L1、L3：还没有交代", "L2：还没有交代", "L4：还没有交代"},
		plan.describe(), "提到了但交代不成立，照常报出")
}

// 追问的输入只列交代上的问题：规划期间新出现的线索不要求交代，也就不出现在追问里。
func TestLeadFollowUp_ListsOnlyTheProblems(t *testing.T) {
	got := leadFollowUp([]leadProblem{{"L1、L3", "还没有交代"}, {"L2", "被重复交代"}})
	want := "上一版规划没有把「待深挖的线索」逐条交代完。" +
		"按「输出契约」重新输出完整的规划 JSON（不是只补差额）：\n" +
		"- L1、L3：还没有交代\n- L2：被重复交代\n"
	assert.Equal(t, want, got)
}

// scriptedProvider 按调用次序返回预设响应的模型替身，记下每一次请求。
type scriptedProvider struct {
	responses []llm.Response
	errs      []error
	requests  []llm.Request
}

func (p *scriptedProvider) Complete(_ context.Context, req llm.Request) (llm.Response, error) {
	p.requests = append(p.requests, req)
	i := len(p.requests) - 1
	if i >= len(p.responses) {
		return llm.Response{}, fmt.Errorf("脚本里没有第 %d 次调用的响应", i+1)
	}
	if i < len(p.errs) && p.errs[i] != nil {
		return llm.Response{}, p.errs[i]
	}
	return p.responses[i], nil
}

func (p *scriptedProvider) Stream(context.Context, llm.Request) (<-chan llm.StreamEvent, error) {
	return nil, llm.ErrStreamingUnsupported
}

func (p *scriptedProvider) ModelID() string    { return "scripted" }
func (p *scriptedProvider) ProviderID() string { return "test" }

const (
	firstPlanJSON  = `{"should_continue":true,"reasoning":"先铺开对象面","actions":[{"instruction":"摸一个新对象面"}]}`
	nudgedPlanJSON = `{"should_continue":true,"reasoning":"补全线索的交代","actions":[{"instruction":"沿甲深挖","pursues":["L1"]}],` +
		`"leads":[{"id":"L2","decision":"close","kind":"not_worth","reason":"价值够不上"},{"id":"L4","decision":"leave"}]}`
)

// firstRound 是追问前那一轮 ReAct 的结果：系统提示、规划输入、模型的第一版输出。
func firstRound() *runtime.ReActResult {
	return &runtime.ReActResult{
		FinalAnswer: firstPlanJSON,
		MessageHistory: []llm.Message{
			{Role: llm.RoleSystem, Content: "章程与契约"},
			{Role: llm.RoleUser, Content: "状态摘要"},
			{Role: llm.RoleAssistant, Content: firstPlanJSON},
		},
		Usage: llm.Usage{InTokens: 1000, OutTokens: 200}, ElapsedMs: 4000, Iterations: 2,
	}
}

func followUpEngine() *Engine {
	e := NewEngine(nil, zerolog.Nop())
	e.compactionTimeouts = runtime.CompactionTimeouts{Distill: time.Minute, Chain: 2 * time.Minute}
	return e
}

// firstPlan 把第一版输出解析成响应，并算出它对线索的交代。
func firstPlan(t *testing.T, planCtx *PlanningContext) (*PlanningResponse, leadPlan) {
	t.Helper()
	var response PlanningResponse
	require.NoError(t, llm.ParseJSON(firstPlanJSON, &response))
	return &response, accountLeads(planCtx, &response)
}

// 没交代完就接着刚才的对话追问一次：模型看得到自己的上一版输出，补全后的输出取代它；追问
// 这次生成的用量与耗时并进这一轮。
func TestCompleteLeadAccounting_TakesTheCompletedAnswer(t *testing.T) {
	planCtx := leadAccountingContext()
	response, plan := firstPlan(t, planCtx)
	require.Len(t, plan.problems, 3)

	provider := &scriptedProvider{responses: []llm.Response{{Content: nudgedPlanJSON, Usage: llm.Usage{InTokens: 1500, OutTokens: 300}}}}
	response, merged, plan, err := followUpEngine().completeLeadAccounting(
		context.Background(), provider, "章程与契约", firstRound(), response, plan, planCtx)
	require.NoError(t, err)

	assert.Equal(t, "补全线索的交代", response.Reasoning, "交下去的是补全后的输出")
	assert.Empty(t, plan.problems, "补全后每条线索都有交代")
	assert.Equal(t, []string{"lead-1", "lead-3"}, plan.claims[0])
	assert.Equal(t, []explorationgraph.LeadClosing{{LeadID: "lead-2", Kind: explorationgraph.ClosureNotWorth, Reason: "价值够不上"}}, plan.closings)
	assert.Equal(t, 1, plan.left)
	assert.Equal(t, nudgedPlanJSON, merged.FinalAnswer, "时间线上这一轮的终稿是补全后的那一版")
	assert.Equal(t, llm.Usage{InTokens: 2500, OutTokens: 500}, merged.Usage, "用量是两次生成之和")
	assert.GreaterOrEqual(t, merged.ElapsedMs, int64(4000), "耗时是两次生成之和")
	assert.Equal(t, 3, merged.Iterations, "不带工具的追问只有一次迭代")
}

// 追问的输入：系统提示只保留一份，历史里的那一份去掉；追问接在模型的第一版输出之后，
// 只陈述哪里没交代成并指回输出契约。
func TestCompleteLeadAccounting_FollowUpContinuesTheConversation(t *testing.T) {
	planCtx := leadAccountingContext()
	response, plan := firstPlan(t, planCtx)

	provider := &scriptedProvider{responses: []llm.Response{{Content: nudgedPlanJSON}}}
	_, _, _, err := followUpEngine().completeLeadAccounting(
		context.Background(), provider, "章程与契约", firstRound(), response, plan, planCtx)
	require.NoError(t, err)
	require.Len(t, provider.requests, 1, "只追问一次")

	messages := provider.requests[0].Messages
	require.Len(t, messages, 4)
	assert.Equal(t, llm.RoleSystem, messages[0].Role, "系统提示在前，且只有这一份")
	assert.Equal(t, []llm.MessageRole{llm.RoleUser, llm.RoleAssistant, llm.RoleUser},
		[]llm.MessageRole{messages[1].Role, messages[2].Role, messages[3].Role})
	assert.Equal(t, firstPlanJSON, messages[2].Content, "模型看得到自己的上一版输出")
	followUp := messages[3].Content
	assert.True(t, strings.HasPrefix(followUp, "上一版规划没有把「待深挖的线索」逐条交代完。按「输出契约」重新输出完整的规划 JSON"), followUp)
	assert.Contains(t, followUp, "\n- L1、L3：还没有交代\n- L2：还没有交代\n- L4：还没有交代\n")
	assert.Empty(t, provider.requests[0].Tools, "追问不带工具")
}

// 追问失败（模型出错、输出不可解析）不作废这一轮规划：按上一版能解析的输出落图，成立的部分
// 照常生效，没交代的线索留待；追问已经花掉的用量照常计入这一轮。
func TestCompleteLeadAccounting_KeepsTheFirstAnswerWhenFollowUpFails(t *testing.T) {
	for name, provider := range map[string]*scriptedProvider{
		"模型出错":   {errs: []error{errors.New("upstream reset")}},
		"输出不可解析": {responses: []llm.Response{{Content: `{"should_continue":true,"actions":[`, Usage: llm.Usage{InTokens: 900}}}},
	} {
		t.Run(name, func(t *testing.T) {
			planCtx := leadAccountingContext()
			firstResponse, firstPlan := firstPlan(t, planCtx)

			response, merged, plan, err := followUpEngine().completeLeadAccounting(
				context.Background(), provider, "章程与契约", firstRound(), firstResponse, firstPlan, planCtx)
			require.NoError(t, err)

			assert.Equal(t, *firstResponse, *response, "按上一版输出落图")
			assert.Equal(t, firstPlan.describe(), plan.describe(), "没交代的线索仍留待")
			assert.Equal(t, firstPlanJSON, merged.FinalAnswer, "时间线上的终稿仍是第一版")
			var spent int
			if len(provider.responses) > 0 {
				spent = provider.responses[0].Usage.InTokens
			}
			assert.Equal(t, 1000+spent, merged.Usage.InTokens, "追问花掉的用量照常计入")
		})
	}
}

// 追问后模型先回了一段散文：运行时会要求它按输出契约重述一次，重述的那一版生效——追问
// 仍只发起一次，多出的那次生成是运行时的格式兜底。
func TestCompleteLeadAccounting_ProseAnswerIsRestatedBeforeParsing(t *testing.T) {
	planCtx := leadAccountingContext()
	response, plan := firstPlan(t, planCtx)

	provider := &scriptedProvider{responses: []llm.Response{
		{Content: "我来补全一下：L1 追、L2 不追。"},
		{Content: nudgedPlanJSON},
	}}
	_, merged, plan, err := followUpEngine().completeLeadAccounting(
		context.Background(), provider, "章程与契约", firstRound(), response, plan, planCtx)
	require.NoError(t, err)
	require.Len(t, provider.requests, 2, "追问一次，加上运行时要求重述的一次")
	assert.Equal(t, nudgedPlanJSON, merged.FinalAnswer)
	assert.Empty(t, plan.problems)
}

// 追问中途这一轮被取消：原样交还错误，不拿半截输出落图。
func TestCompleteLeadAccounting_CancelledRunReturnsTheError(t *testing.T) {
	planCtx := leadAccountingContext()
	response, plan := firstPlan(t, planCtx)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	provider := &scriptedProvider{errs: []error{context.Canceled}}
	_, _, _, err := followUpEngine().completeLeadAccounting(ctx, provider, "章程与契约", firstRound(), response, plan, planCtx)
	require.ErrorIs(t, err, context.Canceled)
}

// 补全后的输出判定停止探索：按停止探索落定，不再安排任何线索。
func TestCompleteLeadAccounting_RespectsStopInFollowUp(t *testing.T) {
	planCtx := leadAccountingContext()
	response, plan := firstPlan(t, planCtx)

	provider := &scriptedProvider{responses: []llm.Response{{Content: `{"should_continue":false,"reasoning":"目标已达成"}`}}}
	response, _, plan, err := followUpEngine().completeLeadAccounting(
		context.Background(), provider, "章程与契约", firstRound(), response, plan, planCtx)
	require.NoError(t, err)
	assert.False(t, response.ShouldContinue)
	assert.Empty(t, plan.claims)
	assert.Empty(t, plan.problems)
}

// 模型写的短号、类别与处置不分大小写、忽略首尾空白。
func TestAccountLeads_NormalizesTokens(t *testing.T) {
	plan := accountLeads(leadAccountingContext(), &PlanningResponse{Leads: []LeadDecisionProposal{
		{ID: " l2 ", Decision: " Close ", Kind: " OUT_OF_SCOPE ", Reason: " 范围外 "},
	}})
	require.Len(t, plan.closings, 1)
	assert.Equal(t, explorationgraph.ClosureOutOfScope, plan.closings[0].Kind)
	assert.Equal(t, "lead-2", plan.closings[0].LeadID)
}
