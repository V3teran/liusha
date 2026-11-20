package toolinvocation

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
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

// 合法 UTF-8 截断后再清洗必须原样保留，不能被误当成其它编码转写成乱码。
func TestPreview_ChineseOutputStaysReadable(t *testing.T) {
	out := "## [1] 诱饵 flag 常自带标记词，勿当战利品提交\n" + strings.Repeat("页面注释中出现的串", 600)

	got := preview(out)

	assert.True(t, utf8.ValidString(got))
	assert.LessOrEqual(t, len(got), previewMax)
	assert.True(t, strings.HasPrefix(got, "## [1] 诱饵 flag 常自带标记词"))
	assert.True(t, strings.HasPrefix(out, got), "预览应是原文的前缀")
}

// 真正不合法的字节只替换掉坏字节，其余内容保留。
func TestPreview_InvalidBytesReplaced(t *testing.T) {
	got := preview("前缀\xff\xfe后缀")

	assert.Equal(t, "前缀�后缀", got)
}
