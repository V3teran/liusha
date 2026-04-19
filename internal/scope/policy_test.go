package scope_test

import (
	"testing"

	"github.com/V3teran/liusha/internal/scope"
	"github.com/stretchr/testify/require"
)

func TestInScope(t *testing.T) {
	rules := scope.Rules{
		Domains:  []string{"example.com", "*.example.com"},
		Excludes: []string{"/admin/*", "/logout"},
		Methods:  []string{"GET", "POST"},
	}
	p, err := scope.NewPolicy(rules)
	require.NoError(t, err)

	cases := []struct {
		name   string
		method string
		rawURL string
		want   bool
	}{
		{"基础域名命中", "GET", "https://example.com/user", true},
		{"通配符子域命中", "POST", "https://api.example.com/v1/x", true},
		{"排除路径前缀", "GET", "https://example.com/admin/users", false},
		{"排除精确路径", "GET", "https://example.com/logout", false},
		{"方法不在白名单", "DELETE", "https://example.com/user", false},
		{"域名不在白名单", "GET", "https://evil.com/user", false},
		{"二级子域不匹配一级通配", "GET", "https://a.b.example.com/x", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := p.InScope(tc.method, tc.rawURL)
			require.Equal(t, tc.want, got)
		})
	}
}

func TestViolationReason(t *testing.T) {
	p, _ := scope.NewPolicy(scope.Rules{Domains: []string{"example.com"}})
	v := p.Violation("GET", "https://evil.com/x")
	require.NotNil(t, v)
	require.Equal(t, "domain_not_allowed", v.Reason)

	v2 := p.Violation("GET", "https://example.com/ok")
	require.Nil(t, v2)
}

func TestEmptyMethodsAllowsAll(t *testing.T) {
	p, _ := scope.NewPolicy(scope.Rules{Domains: []string{"example.com"}})
	require.True(t, p.InScope("DELETE", "https://example.com/x"))
	require.True(t, p.InScope("OPTIONS", "https://example.com/x"))
}

func TestNewPolicy_EmptyDomains(t *testing.T) {
	_, err := scope.NewPolicy(scope.Rules{})
	require.Error(t, err)
}
