package tools

import (
	"slices"
	"testing"

	"github.com/V3teran/liusha/internal/corpus"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/insight"
	"github.com/V3teran/liusha/internal/task"
	"github.com/V3teran/liusha/internal/traffic"
)

// TestBuildToolsWhitelistSemantics 锁定白名单契约：nil = 全量；空 slice = 空集；
// 白名单里不存在的名字静默忽略。与 registry.Allows 的 nil/空区分保持一致。
func TestBuildToolsWhitelistSemantics(t *testing.T) {
	deps := Deps{
		Tasks:      &task.Store{},
		Findings:   &finding.Store{},
		Corpus:     &corpus.Store{},
		Insights:   &insight.Store{},
		ProxyStore: &traffic.ProxyStore{},
		AgentStore: &traffic.AgentStore{},
	}

	all := BuildTools(deps, nil)
	if len(all) == 0 {
		t.Fatal("nil 白名单应返回全量工具，得到 0 个")
	}

	if got := BuildTools(deps, []string{}); len(got) != 0 {
		t.Fatalf("空 slice 应返回空集（严格白名单），得到 %d 个", len(got))
	}

	got := BuildTools(deps, []string{"http_request", "not_a_tool"})
	names := make([]string, 0, len(got))
	for _, tl := range got {
		names = append(names, tl.Name())
	}
	if !slices.Equal(names, []string{"http_request"}) {
		t.Fatalf("白名单过滤结果 = %v， want [http_request]（未知名字应静默忽略）", names)
	}
}
