package tools

import (
	"strings"
	"testing"
)

// 工具边界的流量 id 自描述化：agent_traffic 与 proxy_traffic 独立自增会撞号，
// 裸数字 id 跨表语义不明——引用必须带来源前缀，view 严格路由不做跨表回退。
func TestQualifyAndParseTrafficRef(t *testing.T) {
	if got := qualifyTrafficRef("proxy", 126); got != "proxy:126" {
		t.Fatalf("qualify proxy = %q", got)
	}
	if got := qualifyTrafficRef("agent", 94); got != "agent:94" {
		t.Fatalf("qualify agent = %q", got)
	}

	for _, ref := range []string{"proxy:126", "agent:94"} {
		src, id, err := parseTrafficRef(ref)
		if err != nil {
			t.Fatalf("parse %q: %v", ref, err)
		}
		if !strings.HasPrefix(ref, src+":") {
			t.Fatalf("parse %q 来源前缀不符: %q", ref, src)
		}
		if id <= 0 {
			t.Fatalf("parse %q id 非法: %d", ref, id)
		}
	}
}

func TestParseTrafficRef_Rejects(t *testing.T) {
	cases := []string{
		"126",            // 裸数字（撞号语义不明，必须拒绝）
		"proxy:",         // 缺数字
		"proxy:abc",      // 数字非法
		"proxy:0",        // id 非正
		"proxy:-1",       // id 非正
		"http://x",       // 前缀不是流量来源
		"",               // 空
		"agent:94:extra", // 多段
	}
	for _, ref := range cases {
		if _, _, err := parseTrafficRef(ref); err == nil {
			t.Errorf("非法引用 %q 应被拒绝", ref)
		}
	}
}
