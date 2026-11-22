package distill

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/V3teran/liusha/internal/corpus"
)

func entry(id, title, source string, confirmations int) corpus.Entry {
	return corpus.Entry{ID: id, Title: title, Content: title + "正文", Source: source, Confirmations: confirmations}
}

// 同一已有条目被多个候选引用时共用一个编号；条目注明来源、适用范围与印证数，供判定时掂量。
func TestConsolidationPrompt_SharesRefsAndDescribesEntries(t *testing.T) {
	shared := entry("id-1", "甲", corpus.SourceExpert, 0)
	scoped := entry("id-2", "乙", corpus.SourceAgent, 3)
	code := "ctf"
	scoped.Scenario = &code
	cands := []candidate{
		{lesson: Lesson{Title: "候选一", Content: "一"}, neighbors: []corpus.Entry{shared, scoped}},
		{lesson: Lesson{Title: "候选二", Content: "二"}, neighbors: []corpus.Entry{shared}},
		{lesson: Lesson{Title: "候选三", Content: "三"}},
	}

	prompt, refs := consolidationPrompt(cands)

	require.Len(t, refs, 2)
	assert.Equal(t, "id-1", refs["E1"].ID)
	assert.Equal(t, "id-2", refs["E2"].ID)
	assert.Equal(t, 2, strings.Count(prompt, "- E1（"), "共用编号在两个候选下各列一次")
	assert.Contains(t, prompt, "来源：人工编写，适用范围：通用，0 个任务印证")
	assert.Contains(t, prompt, "来源：任务沉淀，适用范围：场景 ctf，3 个任务印证")
	assert.Contains(t, prompt, "### C3\n标题：候选三\n正文：三\n最接近的已有条目：无")
}

func TestResolveDecisions(t *testing.T) {
	refs := map[string]corpus.Entry{
		"E1": entry("id-1", "甲", corpus.SourceAgent, 1),
		"E2": entry("id-2", "乙", corpus.SourceAgent, 1),
	}
	raw := []rawDecision{
		{Candidate: "C1", Action: "confirm", Targets: []string{"E1"}},
		{Candidate: "C1", Action: "add"}, // 同一候选的第二条判定忽略
		{Candidate: "C2", Action: "revise", Targets: []string{"E9"}},
		{Candidate: "C3", Action: "rename", Targets: []string{"E1"}},
		{Candidate: "C7", Action: "add"},
		{Candidate: " C4 ", Action: "merge", Targets: []string{"E1", "E2", "E1"}, Title: "合", Content: "合并正文"},
		{Candidate: "C5", Action: "merge", Targets: []string{"E1"}}, // 合并至少两条
		{Candidate: "C6", Action: "confirm", Targets: []string{}},   // 印证需要一条
		{Candidate: "C8", Action: "add", Targets: []string{"E1"}},   // 新增不带目标
		{Candidate: "C9", Action: "revise", Targets: []string{"E2"}, Title: "新", Content: "新正文"},
	}

	got, rejected := resolveDecisions(raw, 9, refs)

	assert.Len(t, rejected, 5, "%v", rejected)
	assert.Equal(t, actionConfirm, got[0].action)
	assert.Equal(t, "id-1", got[0].targets[0].ID)
	for _, idx := range []int{1, 2, 4, 5, 7} {
		assert.NotContains(t, got, idx, "不合法的判定不采纳，候选按新增处理")
	}
	assert.Equal(t, decision{action: actionMerge, targets: []corpus.Entry{refs["E1"], refs["E2"]}, title: "合", content: "合并正文"}, got[3], "重复编号去重")
	assert.Equal(t, actionAdd, got[6].action)
	assert.Equal(t, actionRevise, got[8].action)
}
