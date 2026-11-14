package planner

import (
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/explorationgraph"
	"github.com/V3teran/liusha/internal/framework/core"
)

// 观察图状态的「发现」一节按结果的陈述（statement，与假设同名）渲染，优先级取节点层。
// 曾因结果内容另叫 summary，这一节读 statement 读不到，规划者看不到任何坐实的发现。
func TestRenderFindings_ShowsResultStatement(t *testing.T) {
	out := renderFindings([]explorationgraph.Node{{
		ID: "r1", Kind: core.KindResult, Priority: explorationgraph.PriorityHigh,
		Content: []byte(`{"statement":"/search 的 q 参数存在反射型 XSS"}`),
	}})
	if !strings.Contains(out, "[high] /search 的 q 参数存在反射型 XSS") {
		t.Fatalf("应渲染坐实的发现:\n%s", out)
	}
}
