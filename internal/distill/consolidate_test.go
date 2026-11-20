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

// 同一已有条目被多个候选引用时共用一个编号；条目注明来源与印证数，供判定时掂量。
func TestConsolidationPrompt_SharesRefsAndDescribesEntries(t *testing.T) {
	shared := entry("id-1", "诱饵识别", corpus.SourceExpert, 0)
	cands := []candidate{
		{lesson: Lesson{Title: "候选一", Content: "一"}, neighbors: []corpus.Entry{shared, entry("id-2", "基线", corpus.SourceAgent, 3)}},
		{lesson: Lesson{Title: "候选二", Content: "二"}, neighbors: []corpus.Entry{shared}},
		{lesson: Lesson{Title: "候选三", Content: "三"}},
	}

	prompt, refs := consolidationPrompt(cands)

	require.Len(t, refs, 2)
	assert.Equal(t, "id-1", refs["E1"].ID)
	assert.Equal(t, "id-2", refs["E2"].ID)
	assert.Equal(t, 2, strings.Count(prompt, "- E1（"), "共用编号在两个候选下各列一次")
	assert.Contains(t, prompt, "来源：人工录入，0 个任务印证")
	assert.Contains(t, prompt, "来源：任务沉淀，3 个任务印证")
	assert.Contains(t, prompt, "### C3\n标题：候选三\n正文：三\n最接近的已有条目：无")
}

func TestResolveDecisions(t *testing.T) {
	refs := map[string]corpus.Entry{"E1": entry("id-1", "甲", corpus.SourceAgent, 1)}
	raw := []rawDecision{
		{Candidate: "C1", Action: "confirm", Target: "E1"},
		{Candidate: "C1", Action: "add"}, // 同一候选的第二条判定忽略
		{Candidate: "C2", Action: "revise", Target: "E9"},
		{Candidate: "C3", Action: "merge", Target: "E1"},
		{Candidate: "C7", Action: "add"},
		{Candidate: " C4 ", Action: "revise", Target: "E1", Title: "新", Content: "新正文"},
		{Candidate: "C5", Action: "add"},
	}

	got, rejected := resolveDecisions(raw, 5, refs)

	assert.Len(t, rejected, 3, "不存在的条目、不合法的动作、不存在的候选各一条")
	assert.Equal(t, actionConfirm, got[0].action)
	assert.Equal(t, "id-1", got[0].target.ID)
	assert.NotContains(t, got, 1, "指向不存在条目的判定不采纳，候选按新增处理")
	assert.NotContains(t, got, 2)
	assert.Equal(t, decision{action: actionRevise, target: refs["E1"], title: "新", content: "新正文"}, got[3])
	assert.Equal(t, actionAdd, got[4].action)
}
