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

	// 索引 = 寻址键 + 显示名 + 描述（刻意不含 category——页面组织层不进 prompt）
	for _, want := range []string{
		"- dom-xss（DOM XSS 检测指南）: source→sink 链",
		"- noshow（无分类技能）: 描述文本",
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

// ["*"] 全量哨兵（executor 种子语义）：全部放行。此前无哨兵处理导致
// executor 的技能索引恒为空（allow 集合只含字面量 "*"）。
func TestAllow_WildcardAllowsAll(t *testing.T) {
	v := Allow(NewStoreReader(testSource()), []string{"*"})
	got, err := v.Metas(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Errorf(`["*"] 应全量放行, got %d 张卡`, len(got))
	}
}

// 哨兵与显式名单混写时同为全量（任一 "*" 即放行，配置漂移不致命）。
func TestAllow_WildcardMixed(t *testing.T) {
	v := Allow(NewStoreReader(testSource()), []string{"bac", "*"})
	got, err := v.Metas(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	// 夹具启用项共 2 张（dom-xss/bac）——全量放行。
	if len(got) != 2 {
		t.Errorf("混写哨兵应全量放行, got %d 张卡", len(got))
	}
}
