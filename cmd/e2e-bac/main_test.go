package main

import (
	"net/http"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/vulnfinding"
)

// 13 个调用 = profile×3 + order/7×3 + admin/users×3 + admin/delete×3 + 1 anonymous。
const wantProxyRequests = 13

// TestProxyRequests_HasExpectedCount 锁住调用集大小，改动需同步调整 e2e 脚本预算。
func TestProxyRequests_HasExpectedCount(t *testing.T) {
	got := proxyRequests()
	if len(got) != wantProxyRequests {
		t.Fatalf("proxyRequests() len = %d, want %d", len(got), wantProxyRequests)
	}
}

// TestProxyRequests_AllValid 校验每条记录字段完整性：method 仅 GET/POST、path 以 / 开头、
// 仅 anonymous 允许 Sess 为空、有 body 必须是 POST。
func TestProxyRequests_AllValid(t *testing.T) {
	for i, c := range proxyRequests() {
		if c.Method != http.MethodGet && c.Method != http.MethodPost {
			t.Errorf("[%d] %s: method = %q, want GET or POST", i, c.Name, c.Method)
		}
		if c.Path == "" || c.Path[0] != '/' {
			t.Errorf("[%d] %s: path = %q, want absolute path", i, c.Name, c.Path)
		}
		if c.Sess == "" && c.Name != "anonymous" {
			t.Errorf("[%d] %s: empty Sess but name != anonymous", i, c.Name)
		}
		if c.Body != "" && c.Method != http.MethodPost {
			t.Errorf("[%d] %s: body present but method = %s", i, c.Name, c.Method)
		}
	}
}

// TestProxyRequests_Covers4Endpoints 锁住"4 类 BAC 场景"覆盖度（baseline + 三类越权）——
// 即使后续调整调用总数，4 类端点也必须保留，否则 worker 出不齐 3 类 finding。
func TestProxyRequests_Covers4Endpoints(t *testing.T) {
	want := []string{
		"/api/bac/profile",       // baseline
		"/api/bac/order/7",       // horizontal_priv_esc
		"/api/bac/admin/users",   // vertical_priv_esc
		"/api/bac/admin/delete",  // unauthorized_access
	}
	got := proxyRequests()
	for _, prefix := range want {
		hit := false
		for _, c := range got {
			if strings.HasPrefix(c.Path, prefix) {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("缺少 BAC 场景 endpoint: %s", prefix)
		}
	}
}

// TestFilterBAC 验证过滤器：仅保留 kind 以 "bac." 开头的 finding；
// 同时验证空切片不 panic、纯非 BAC 切片返回空。
func TestFilterBAC(t *testing.T) {
	tests := []struct {
		name string
		in   []vulnfinding.VulnFinding
		want int
	}{
		{
			name: "nil 切片",
			in:   nil,
			want: 0,
		},
		{
			name: "纯 BAC",
			in: []vulnfinding.VulnFinding{
				{Kind: "bac.horizontal_priv_esc"},
				{Kind: "bac.vertical_priv_esc"},
				{Kind: "bac.idor_read"},
			},
			want: 3,
		},
		{
			name: "纯非 BAC",
			in: []vulnfinding.VulnFinding{
				{Kind: "leak.api_key"},
				{Kind: "debug.endpoint"},
			},
			want: 0,
		},
		{
			name: "混合",
			in: []vulnfinding.VulnFinding{
				{Kind: "bac.horizontal_priv_esc"},
				{Kind: "leak.api_key"},
				{Kind: "bac.idor_write"},
				{Kind: "debug.endpoint"},
				{Kind: "bac.vertical_priv_esc"},
			},
			want: 3,
		},
		{
			name: "前缀近似但不匹配（bac 不带点）",
			in: []vulnfinding.VulnFinding{
				{Kind: "background.scan"},
				{Kind: "bac"},
			},
			want: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterBAC(tc.in)
			if len(got) != tc.want {
				t.Fatalf("filterBAC() len = %d, want %d", len(got), tc.want)
			}
			for _, f := range got {
				if !strings.HasPrefix(f.Kind, "bac.") {
					t.Errorf("filterBAC() 返回非 bac.* kind: %q", f.Kind)
				}
			}
		})
	}
}

// TestCountKinds 锁住"至少 N 类齐全"门槛的核心计数语义。
func TestCountKinds(t *testing.T) {
	in := []vulnfinding.VulnFinding{
		{Kind: "bac.horizontal_priv_esc"},
		{Kind: "bac.horizontal_priv_esc"},
		{Kind: "bac.vertical_priv_esc"},
		{Kind: "bac.idor_read"},
	}
	got := countKinds(in)
	if len(got) != 3 {
		t.Fatalf("countKinds() 类别数 = %d, want 3", len(got))
	}
	if got["bac.horizontal_priv_esc"] != 2 {
		t.Errorf("horizontal_priv_esc 计数 = %d, want 2", got["bac.horizontal_priv_esc"])
	}
	if got["bac.vertical_priv_esc"] != 1 {
		t.Errorf("vertical_priv_esc 计数 = %d, want 1", got["bac.vertical_priv_esc"])
	}
	if got["bac.idor_read"] != 1 {
		t.Errorf("idor_read 计数 = %d, want 1", got["bac.idor_read"])
	}
}

// TestEnvOr 验证：env 已设取 env，未设/空串取默认。
func TestEnvOr(t *testing.T) {
	const k = "LIUSHA_E2E_BAC_TEST_KEY"
	t.Setenv(k, "")
	if got := envOr(k, "default"); got != "default" {
		t.Errorf("envOr(空) = %q, want default", got)
	}
	t.Setenv(k, "override")
	if got := envOr(k, "default"); got != "override" {
		t.Errorf("envOr(set) = %q, want override", got)
	}
}
