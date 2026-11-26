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
	drafts := e.convertProposals(ctx, store, "t1", []ActionProposal{{
		Instruction: "复测 /search", Complexity: "complex", Priority: "high",
		Reasoning: "反射点已坐实", TriggeredBy: []string{resultID, "phantom"}, PursuesLead: " lead-1 ",
	}})

	require.Len(t, drafts, 1)
	d := drafts[0]
	assert.Equal(t, explorationgraph.PriorityHigh, d.Node.Priority)
	require.NotNil(t, d.Node.Complexity)
	assert.Equal(t, explorationgraph.ComplexityComplex, *d.Node.Complexity)
	assert.Equal(t, []string{resultID}, d.TriggeredBy, "幻影引用剔除")
	assert.Equal(t, "lead-1", d.PursuesLead, "认领的线索随草稿交给落图（落成 pursues 边）")

	var content map[string]any
	require.NoError(t, json.Unmarshal(d.Node.Content, &content))
	assert.Equal(t, map[string]any{"instruction": "复测 /search", "reasoning": "反射点已坐实"}, content)
}

// 不追提案转成图修订的处置：ID 去空白，没有 ID 的条目丢弃；理由原样交下去，成不成立由
// 修订校验判定。
func TestConvertLeadDeclines(t *testing.T) {
	got := convertLeadDeclines([]LeadDeclineProposal{
		{ID: " lead-1 ", Reason: "超出任务目标"},
		{ID: "   ", Reason: "没有 ID"},
		{ID: "lead-2"},
	})
	require.Len(t, got, 2)
	assert.Equal(t, explorationgraph.LeadDecline{LeadID: "lead-1", Reason: "超出任务目标"}, got[0])
	assert.Equal(t, explorationgraph.LeadDecline{LeadID: "lead-2"}, got[1])
}

// 时间线叙述：有动作时列动作；判定不追的线索另起一行列理由；只有不追、没有动作时不再
// 写「派发 0 个动作」。
func TestPlanningNarrative(t *testing.T) {
	base := "继续执行，本轮派发 1 个动作：\n1. 深挖参数甲"
	if got := planningNarrative(PlanningResponse{ShouldContinue: true, Actions: []ActionProposal{{Instruction: "深挖参数甲"}}}); got != base {
		t.Fatalf("只有动作时得:\n%s", got)
	}
	if got := planningNarrative(PlanningResponse{ShouldContinue: true}); got != "继续执行，本轮派发 0 个动作：" {
		t.Fatalf("没有动作也没有不追时保持原样，得:\n%s", got)
	}

	got := planningNarrative(PlanningResponse{ShouldContinue: true, Actions: []ActionProposal{{Instruction: "深挖参数甲"}},
		DeclinedLeads: []LeadDeclineProposal{{Reason: "超出任务目标"}, {Reason: "前提已证伪"}}})
	want := base + "\n判定不追的线索：\n- 超出任务目标\n- 前提已证伪"
	if got != want {
		t.Fatalf("有动作与不追时得:\n%s\n期望:\n%s", got, want)
	}
	got = planningNarrative(PlanningResponse{ShouldContinue: true, DeclinedLeads: []LeadDeclineProposal{{Reason: "超出任务目标"}}})
	if got != "判定不追的线索：\n- 超出任务目标" {
		t.Fatalf("只有不追时得:\n%s", got)
	}
	if got := planningNarrative(PlanningResponse{}); got != "任务收敛，不再派发动作。" {
		t.Fatalf("停止探索时得:\n%s", got)
	}
}
