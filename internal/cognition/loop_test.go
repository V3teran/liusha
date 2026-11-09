package cognition

import (
	"slices"
	"testing"
)

// TestIntersectPlannerSafeTools 锁定规划者函数工具的上界：声明名单（语义同 registry.Allows）
// 与规划安全集求交，执行面工具无论怎么配都进不来。
func TestIntersectPlannerSafeTools(t *testing.T) {
	cases := []struct {
		name      string
		whitelist []string
		want      []string
	}{
		{"nil 默认拒绝", nil, []string{}},
		{"空名单为空集", []string{}, []string{}},
		{"全部 = 安全集全部", []string{"*"}, []string{"evaluate_progress", "observe_state", "search_corpus"}},
		{"执行面工具被挡下", []string{"search_corpus", "run_command", "http_request"}, []string{"search_corpus"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := intersectPlannerSafeTools(tc.whitelist); !slices.Equal(got, tc.want) {
				t.Fatalf("intersectPlannerSafeTools(%v) = %v, want %v", tc.whitelist, got, tc.want)
			}
		})
	}
}
