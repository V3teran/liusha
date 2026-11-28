package toolinvocation

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 截断落在多字节字符中间时退回到字符边界，不能留下半个字符。
func TestTruncateUTF8_KeepsRuneBoundary(t *testing.T) {
	s := strings.Repeat("诱", 3) // 每个 3 字节
	for limit := 0; limit <= len(s)+1; limit++ {
		got := truncateUTF8(s, limit)
		assert.True(t, utf8.ValidString(got), "limit=%d", limit)
		assert.LessOrEqual(t, len(got), limit, "limit=%d", limit)
		assert.Equal(t, min(limit, len(s))/3*3, len(got), "limit=%d 应保留全部完整字符", limit)
	}
}

// 预览超过上限时留开头与结尾、中间写明省略多少字节：结尾是命令结果里退出码、完整输出路径所在。
// 合法 UTF-8 清洗后原样保留，不能被误当成其它编码转写成乱码。
func TestPreview_KeepsHeadAndTailWithOmissionNote(t *testing.T) {
	out := "## [1] 诱饵 flag 常自带标记词，勿当战利品提交\n" + strings.Repeat("页面注释中出现的串", 600)

	got := preview(out)

	assert.True(t, utf8.ValidString(got))
	assert.LessOrEqual(t, len(got), previewMax)
	i := strings.Index(got, "…（中间省略 ")
	require.Greater(t, i, 0, "中间应有省略说明: %q", got[:80])
	j := strings.Index(got[i:], " 字节）…")
	require.GreaterOrEqual(t, j, 0)
	j += i + len(" 字节）…")
	head, note, tail := got[:i], got[i:j], got[j:]
	assert.True(t, strings.HasPrefix(out, head), "说明之前应是原文的前缀")
	assert.True(t, strings.HasSuffix(out, tail), "说明之后应是原文的结尾")
	omitted, err := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(note, "…（中间省略 "), " 字节）…"))
	require.NoError(t, err)
	assert.Equal(t, len(out)-len(head)-len(tail), omitted, "说明里的省略量对得上")
}

// 真正不合法的字节只替换掉坏字节，其余内容保留。
func TestPreview_InvalidBytesReplaced(t *testing.T) {
	got := preview("前缀\xff\xfe后缀")

	assert.Equal(t, "前缀�后缀", got)
}
