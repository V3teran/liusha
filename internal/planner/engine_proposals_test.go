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
		Content: []byte(`{"summary":"r"}`)})
	require.NoError(t, err)

	e := NewEngine(nil, zerolog.Nop())
	drafts := e.convertProposals(ctx, store, "t1", []ActionProposal{{
		Type: "testing", Instruction: "复测 /search", Complexity: "complex", Priority: "high",
		Reasoning: "反射点已坐实", TriggeredBy: []string{resultID, "phantom"},
	}})

	require.Len(t, drafts, 1)
	d := drafts[0]
	assert.Equal(t, explorationgraph.PriorityHigh, d.Node.Priority)
	require.NotNil(t, d.Node.Complexity)
	assert.Equal(t, explorationgraph.ComplexityComplex, *d.Node.Complexity)
	assert.Equal(t, []string{resultID}, d.TriggeredBy, "幻影引用剔除")

	var content map[string]any
	require.NoError(t, json.Unmarshal(d.Node.Content, &content))
	assert.Equal(t, map[string]any{"type": "testing", "instruction": "复测 /search", "reasoning": "反射点已坐实"}, content)
}
