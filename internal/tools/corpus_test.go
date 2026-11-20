package tools

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/V3teran/liusha/internal/corpus"
)

func TestRenderSearchResult(t *testing.T) {
	hits := []corpus.Hit{
		{Entry: corpus.Entry{Title: "诱饵识别", Content: "带 fake 字样的是诱饵", Source: corpus.SourceAgent, Confirmations: 3}},
		{Entry: corpus.Entry{Title: "基线", Content: "先建 404 基线", Source: corpus.SourceExpert}},
	}

	got := renderSearchResult(corpus.Result{Mode: corpus.ModeSemantic, Hits: hits})

	assert.Equal(t, "检索方式：语义相关度\n\n"+
		"## [1] 诱饵识别\n来源：任务沉淀，3 个任务印证\n带 fake 字样的是诱饵\n\n"+
		"## [2] 基线\n来源：人工录入\n先建 404 基线", got)
}

// 语义检索故障退回字面检索时要注明，空结果也写明检索方式。
func TestRenderSearchResult_FallbackAndEmpty(t *testing.T) {
	got := renderSearchResult(corpus.Result{Mode: corpus.ModeLexical, Fallback: "重排: timeout"})

	assert.Equal(t, "检索方式：字面匹配（语义检索暂不可用）\n\n未找到相关知识条目。", got)
	assert.Equal(t, "检索方式：字面匹配\n\n未找到相关知识条目。", renderSearchResult(corpus.Result{Mode: corpus.ModeLexical}))
}
