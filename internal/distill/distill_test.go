package distill

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

func texts(ms []material) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = m.ref + "|" + fmt.Sprint(m.detour) + "|" + m.text
	}
	return out
}

// 桶优先级：弯路全保优先，其后结果、事实、有产出的动作；预算耗尽后低优先级的桶让位并留痕；
// 编号按最终顺序。
func TestAssembleMaterials_PriorityAndNumbering(t *testing.T) {
	facts := make([]string, 400)
	for i := range facts {
		facts[i] = fmt.Sprintf("确认的事实：F%03d", i) + strings.Repeat("x", 80)
	}
	got := assembleMaterials([]string{"证伪的主张：H1"}, []string{"坐实的结果：R1"}, facts, []string{"有产出的动作：A1"})

	require.GreaterOrEqual(t, len(got), 3)
	assert.Equal(t, material{ref: "M1", detour: true, text: "证伪的主张：H1"}, got[0])
	assert.Equal(t, material{ref: "M2", text: "坐实的结果：R1"}, got[1])
	joined := strings.Join(texts(got), "\n")
	assert.Contains(t, joined, "F399", "桶内保留最新")
	assert.NotContains(t, joined, "F000")
	assert.NotContains(t, joined, "A1", "预算耗尽后低优先级的桶整体让位")
	last := got[len(got)-1]
	assert.Empty(t, last.ref)
	assert.Contains(t, last.text, "未列出")
}

func TestAssembleMaterials_TruncatesLongItems(t *testing.T) {
	got := assembleMaterials([]string{strings.Repeat("长", materialMaxRunes+50)})
	require.Len(t, got, 1)
	assert.LessOrEqual(t, len([]rune(got[0].text)), materialMaxRunes+len([]rune("…（截断）")))
	assert.True(t, hasDetour(got))
	assert.False(t, hasDetour(assembleMaterials(nil, []string{"坐实的结果：R"})))
}

func TestRenderMaterials(t *testing.T) {
	got := renderMaterials([]material{
		{ref: "M1", detour: true, text: "失败的动作：甲"},
		{ref: "M2", text: "确认的事实：乙"},
		{text: "（另有 3 条较早的材料因体量上限未列出）"},
	})
	assert.Equal(t, "[M1]【弯路】失败的动作：甲\n[M2] 确认的事实：乙\n（另有 3 条较早的材料因体量上限未列出）\n", got)
}

// 材料覆盖：证伪的主张（带原因与说明）、事实、结果；动作按结局分类——失败、被否决、做完却
// 没有产出的是弯路，有产出的附产出数；被中止与未结束的动作不入材料。
func TestCollect_ClassifiesTrajectory(t *testing.T) {
	ctx := context.Background()
	g := explorationgraph.NewMemoryStore()
	state := func(s explorationgraph.State) *explorationgraph.State { return &s }
	vstatus := func(v explorationgraph.VerificationStatus) *explorationgraph.VerificationStatus { return &v }
	reason := "超时"
	nodes := []explorationgraph.Node{
		{ID: "a-fail", Kind: core.KindAction, State: state(explorationgraph.StateFailed), StateReason: &reason, Content: json.RawMessage(`{"instruction":"失败的事"}`)},
		{ID: "a-rej", Kind: core.KindAction, State: state(explorationgraph.StateRejected), Content: json.RawMessage(`{"instruction":"不该做的事"}`)},
		{ID: "a-empty", Kind: core.KindAction, State: state(explorationgraph.StateCompleted), Content: json.RawMessage(`{"instruction":"白做的事"}`)},
		{ID: "a-yield", Kind: core.KindAction, State: state(explorationgraph.StateCompleted), Content: json.RawMessage(`{"instruction":"有用的事"}`)},
		{ID: "a-abort", Kind: core.KindAction, State: state(explorationgraph.StateAborted), Content: json.RawMessage(`{"instruction":"被收回的事"}`)},
		{ID: "a-open", Kind: core.KindAction, State: state(explorationgraph.StateOpen), Content: json.RawMessage(`{"instruction":"还没做的事"}`)},
		{ID: "o-fact", Kind: core.KindObservation, Content: json.RawMessage(`{"statement":"事实一"}`)},
		{ID: "o-fact2", Kind: core.KindObservation, Content: json.RawMessage(`{"statement":"事实二"}`)},
		{ID: "o-ref", Kind: core.KindObservation, VerificationStatus: vstatus(explorationgraph.VerificationRefuted),
			Content: json.RawMessage(`{"statement":"错的主张"}`), Metadata: json.RawMessage(`{"refute_reason":"claim_invalid","refute_detail":"证据不足"}`)},
		{ID: "o-pend", Kind: core.KindObservation, VerificationStatus: vstatus(explorationgraph.VerificationPending), Content: json.RawMessage(`{"statement":"待验证"}`)},
		{ID: "r1", Kind: core.KindResult, Content: json.RawMessage(`{"statement":"结果一"}`)},
	}
	producedBy := map[string]string{"o-fact": "a-yield", "o-fact2": "a-yield", "o-ref": "a-fail"}
	for _, n := range nodes { // 动作在前：观察连同产出它的边一起写入
		n.TaskID = "t"
		var edges []explorationgraph.Edge
		if src, ok := producedBy[n.ID]; ok {
			edges = append(edges, explorationgraph.Edge{TaskID: "t", SrcID: src, Rel: explorationgraph.RelGenerates, DstID: n.ID})
		}
		require.NoError(t, g.CreateNodeWithEdges(ctx, n, edges))
	}

	s := &Service{graph: g, logger: zerolog.Nop()}
	got := strings.Join(texts(s.collect(ctx, "t")), "\n")

	for _, want := range []string{
		"|true|证伪的主张：错的主张（原因：claim_invalid；说明：证据不足）",
		"|true|失败的动作：失败的事（原因：超时）",
		"|true|被否决的动作：不该做的事",
		"|true|没有任何产出的动作：白做的事",
		"|false|坐实的结果：结果一",
		"|false|确认的事实：事实一",
		"|false|有产出的动作（2 条观察）：有用的事",
	} {
		assert.Contains(t, got, want)
	}
	for _, absent := range []string{"被收回的事", "还没做的事", "待验证"} {
		assert.NotContains(t, got, absent)
	}
}

func TestAdmit_EvidenceGate(t *testing.T) {
	ms := []material{
		{ref: "M1", detour: true, text: "证伪的主张：甲"},
		{ref: "M2", text: "坐实的结果：乙"},
	}
	lessons := []Lesson{
		{Title: "有弯路为证", Content: "正文", Scoped: true, Evidence: []string{"M1", "[M2]", "M9"}},
		{Title: "只引用走通的路", Content: "正文", Evidence: []string{"M2"}},
		{Title: "没有引用", Content: "正文"},
		{Title: "引用不存在", Content: "正文", Evidence: []string{"M7"}},
		{Title: " ", Content: "正文", Evidence: []string{"M1"}},
		{Title: "二", Content: "正文", Evidence: []string{"M1"}},
		{Title: "三", Content: "正文", Evidence: []string{"M1"}},
		{Title: "超出上限", Content: "正文", Evidence: []string{"M1"}},
	}

	kept, dropped := admit(lessons, ms, true)

	require.Len(t, kept, maxLessons)
	assert.Equal(t, Lesson{Title: "有弯路为证", Content: "正文", Scoped: true, Evidence: []string{"M1", "M2"}}, kept[0], "无效编号剔除")
	assert.Equal(t, []string{"有弯路为证", "二", "三"}, []string{kept[0].Title, kept[1].Title, kept[2].Title})
	assert.Len(t, dropped, 5)

	kept, _ = admit(lessons[:1], ms, false)
	assert.False(t, kept[0].Scoped, "没有任务场景时一律为通用")
}
