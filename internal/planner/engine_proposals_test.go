package planner

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// 规划提案转成动作草稿：优先级与复杂度只进节点层，因果引用随草稿交给落图（落成
// triggers 边），内容只留动作本身——类型、指令与规划理由。
func TestConvertProposals_ContentHoldsOnlyTheAction(t *testing.T) {
	ctx := context.Background()
	store := explorationgraph.NewMemoryStore()
	resultID := "11111111-2222-4333-8444-555555555555"
	_, err := store.CreateNode(ctx, explorationgraph.Node{ID: resultID, TaskID: "t1", Kind: core.KindResult,
		Content: []byte(`{"statement":"r"}`)})
	require.NoError(t, err)

	e := NewEngine(nil, zerolog.Nop())
	planCtx := &PlanningContext{}
	drafts := e.convertProposals(ctx, store, "t1", planCtx, []ActionProposal{
		{
			Instruction: "复测 /search", Complexity: "complex", Urgent: true,
			Reasoning: "反射点已坐实", TriggeredBy: []string{resultID, "phantom"},
		},
		{Instruction: "另一步", Complexity: "simple", Reasoning: "铺开"},
	})

	require.Len(t, drafts, 2)
	d := drafts[0]
	assert.Empty(t, d.Node.Priority, "规划者排出的动作不带优先级")
	assert.True(t, d.Node.Urgent, "加急落在动作上")
	assert.False(t, drafts[1].Node.Urgent)
	require.NotNil(t, d.Node.Complexity)
	assert.Equal(t, explorationgraph.ComplexityComplex, *d.Node.Complexity)
	assert.Equal(t, []string{resultID}, d.TriggeredBy, "幻影引用剔除")

	var content map[string]any
	require.NoError(t, json.Unmarshal(d.Node.Content, &content))
	assert.Equal(t, map[string]any{"instruction": "复测 /search", "reasoning": "反射点已坐实"}, content)

}

// 所属方向只认「进行中的方向」里的编号：解析成那个派生目标的 ID 随草稿交给落图。没声明的、
// 引用失效的（不在清单里、写的是 ID 而不是编号）都挂在任务目标下——照常排出这一步，只是
// 来路上记不到方向。
func TestConvertProposals_DirectionResolvesToTheOwningObjective(t *testing.T) {
	ctx := context.Background()
	store := explorationgraph.NewMemoryStore()
	e := NewEngine(nil, zerolog.Nop())
	planCtx := &PlanningContext{Directions: []explorationgraph.Node{{ID: "d-one"}, {ID: "d-two"}}}

	drafts := e.convertProposals(ctx, store, "t1", planCtx, []ActionProposal{
		{Instruction: "甲", Direction: "D2"},
		{Instruction: "乙", Direction: " d1 "},
		{Instruction: "丙"},
		{Instruction: "丁", Direction: "D9"},
		{Instruction: "戊", Direction: "d-one"},
	})

	require.Len(t, drafts, 5, "方向引用失效不拦动作")
	owners := make([]string, 0, len(drafts))
	for _, d := range drafts {
		owners = append(owners, d.ObjectiveID)
	}
	assert.Equal(t, []string{"d-two", "d-one", "", "", ""}, owners)
}

// 时间线叙述：有动作时列动作；关闭的线索每条一行；留待的不写（图上没有变化）；只有关闭、
// 没有动作时不再写「派发 0 个动作」。
func TestPlanningNarrative(t *testing.T) {
	base := "继续执行，本轮派发 1 个动作：\n1. 深挖参数甲"
	if got := planningNarrative(PlanningResponse{ShouldContinue: true, Actions: []ActionProposal{{Instruction: "深挖参数甲"}}}); got != base {
		t.Fatalf("只有动作时得:\n%s", got)
	}
	if got := planningNarrative(PlanningResponse{ShouldContinue: true, Actions: []ActionProposal{{Instruction: "先续会话", Urgent: true}}}); got != "继续执行，本轮派发 1 个动作：\n1. 先续会话（加急）" {
		t.Fatalf("加急的动作在叙述里标出来，得:\n%s", got)
	}
	if got := planningNarrative(PlanningResponse{ShouldContinue: true}); got != "继续执行，本轮派发 0 个动作：" {
		t.Fatalf("没有动作也没有处置时保持原样，得:\n%s", got)
	}
	if got := planningNarrative(PlanningResponse{ShouldContinue: true, Leads: []LeadDecisionProposal{{ID: "L1", Decision: "leave"}}}); got != "继续执行，本轮派发 0 个动作：" {
		t.Fatalf("只留待与没有处置是同一局面，得:\n%s", got)
	}

	leads := []LeadDecisionProposal{
		{ID: "L3", Decision: "close", Kind: "out_of_scope", Reason: "范围外"},
		{ID: "L4", Decision: "close", Kind: "blocked", Reason: "需要管理员凭据"},
	}
	rest := "关闭的线索（超出任务目标）：范围外\n关闭的线索（受阻）：需要管理员凭据"
	if got := planningNarrative(PlanningResponse{ShouldContinue: true, Actions: []ActionProposal{{Instruction: "深挖参数甲"}}, Leads: leads}); got != base+"\n"+rest {
		t.Fatalf("有动作与处置时得:\n%s\n期望:\n%s", got, base+"\n"+rest)
	}
	if got := planningNarrative(PlanningResponse{ShouldContinue: true, Leads: leads}); got != rest {
		t.Fatalf("只有处置时不写派发 0 个动作，得:\n%s", got)
	}
	if got := planningNarrative(PlanningResponse{}); got != "任务收敛，不再派发动作。" {
		t.Fatalf("停止探索时得:\n%s", got)
	}
}
