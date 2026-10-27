package skill

import (
	"strings"
	"testing"
)

// RenderIndex 是 Tier 1 技能索引进 system prompt 的唯一渲染：
// 寻址键 + 显示名 + category 标签 + 描述，供 executor/evaluator 口径一致。
func TestRenderIndex(t *testing.T) {
	cards := []*Card{
		{Key: "dom-xss", Name: "DOM XSS 检测指南", Category: "vuln", Description: "source→sink 链"},
		{Key: "noshow", Name: "无分类技能", Description: "描述文本"},
	}
	out := RenderIndex(cards)

	for _, want := range []string{
		"- dom-xss（DOM XSS 检测指南） [vuln]: source→sink 链",
		"- noshow（无分类技能）: 描述文本", // 无 category 不渲染空标签
	} {
		if !strings.Contains(out, want) {
			t.Errorf("索引缺行 %q:\n%s", want, out)
		}
	}
}

func TestRenderIndex_Empty(t *testing.T) {
	if out := RenderIndex(nil); out != "" {
		t.Errorf("空索引应渲染空串, got %q", out)
	}
}
