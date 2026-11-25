package cognition

import (
	"slices"
	"testing"
)

// TestIntersectPlannerSafeTools 锁定规划者函数工具的上界：声明名单（语义同 allowlist.Allows）
// 与规划安全集求交，执行面工具无论怎么配都进不来。
func TestIntersectPlannerSafeTools(t *testing.T) {
	cases := []struct {
		name      string
		whitelist []string
		want      []string
	}{
		{"nil 默认拒绝", nil, []string{}},
		{"空名单为空集", []string{}, []string{}},
		{"全部 = 安全集全部", []string{"*"}, []string{"progress_evaluate", "graph_read", "corpus_search"}},
		{"执行面工具被挡下", []string{"corpus_search", "command_run", "http_request"}, []string{"corpus_search"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := intersectPlannerSafeTools(tc.whitelist); !slices.Equal(got, tc.want) {
				t.Fatalf("intersectPlannerSafeTools(%v) = %v, want %v", tc.whitelist, got, tc.want)
			}
		})
	}
}
