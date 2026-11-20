package corpus

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitMarkdown_HeadingsAndPreamble(t *testing.T) {
	md := "开篇说明\n第二行\n\n# 侦察\n先看 robots.txt\n\n## 只有标题\n\n### 基线 ###\n先建 404 基线\n#不是标题\n"

	got := SplitMarkdown(md)

	assert.Equal(t, []Section{
		{Title: "开篇说明", Content: "开篇说明\n第二行"},
		{Title: "侦察", Content: "先看 robots.txt"},
		{Title: "基线", Content: "先建 404 基线\n#不是标题"},
	}, got)
}

// 代码围栏里的「# 注释」不是标题，不能把代码切碎。
func TestSplitMarkdown_IgnoresHeadingsInFences(t *testing.T) {
	md := "# 探测\n```sh\n# 先列目录\nls\n```\n~~~\n# 也是注释\n~~~\n结尾"

	got := SplitMarkdown(md)

	require.Len(t, got, 1)
	assert.Equal(t, "探测", got[0].Title)
	assert.Contains(t, got[0].Content, "# 先列目录")
	assert.Contains(t, got[0].Content, "# 也是注释")
}

// 超长的节按段落切成多条，每条不超过正文上限，标题标注序号。
func TestSplitMarkdown_SplitsLongSections(t *testing.T) {
	para := strings.Repeat("段", 400)
	longLine := strings.Repeat("行", MaxContentRunes+10)
	md := "# 长节\n" + para + "\n\n" + para + "\n\n" + para + "\n\n" + longLine

	got := SplitMarkdown(md)

	require.Len(t, got, 4)
	for i, s := range got {
		assert.LessOrEqual(t, utf8.RuneCountInString(s.Content), MaxContentRunes, "第 %d 段", i+1)
		assert.LessOrEqual(t, utf8.RuneCountInString(s.Title), MaxTitleRunes)
	}
	assert.Equal(t, "长节（1/4）", got[0].Title)
	assert.Equal(t, para+"\n\n"+para, got[0].Content, "段落尽量合并到上限以内")
	assert.Equal(t, "长节（4/4）", got[3].Title)
	assert.Equal(t, strings.Repeat("行", 10), got[3].Content, "过长的行按字数硬切")
}

func TestKeep_ThresholdAndLimit(t *testing.T) {
	hits := []Hit{{Score: 0.4}, {Score: 0.9}, {Score: 0.6}, {Score: 0.7}}

	got := keep(hits, 0.5, 2)

	require.Len(t, got, 2)
	assert.Equal(t, 0.9, got[0].Score)
	assert.Equal(t, 0.7, got[1].Score)
	assert.Empty(t, keep([]Hit{{Score: 0.49}}, 0.5, 5), "门槛以下宁可为空")
}

func TestDraftNormalize(t *testing.T) {
	blank := "  "
	d, err := Draft{Title: " 标题 ", Content: " 正文 ", Scenario: &blank, Source: SourceExpert}.Normalize()
	require.NoError(t, err)
	assert.Equal(t, Draft{Title: "标题", Content: "正文", Source: SourceExpert}, d, "空白场景即通用")

	_, err = Draft{Title: strings.Repeat("标", MaxTitleRunes+1), Content: "x", Source: SourceExpert}.Normalize()
	assert.ErrorIs(t, err, ErrInvalid)
}
