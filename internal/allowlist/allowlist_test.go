package allowlist

import "testing"

func TestAllows(t *testing.T) {
	cases := []struct {
		name  string
		names []string
		item  string
		want  bool
	}{
		{"nil 默认拒绝", nil, "a", false},
		{"空名单为空集", []string{}, "a", false},
		{"通配放行全部", []string{"*"}, "a", true},
		{"含通配即全部", []string{"b", "*"}, "a", true},
		{"名单内放行", []string{"a", "b"}, "a", true},
		{"名单外拒绝", []string{"b"}, "a", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Allows(tc.names, tc.item); got != tc.want {
				t.Fatalf("Allows(%v, %q) = %v, want %v", tc.names, tc.item, got, tc.want)
			}
		})
	}
}
