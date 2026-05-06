package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/vulnfinding"
)

// 4 条样本：profile (baseline) + order/7 (horizontal) + admin/users (vertical) + admin/delete (unauthorized)。
const wantSamples = 4

// TestSampleFile_HasExpectedCount 锁住样本数量；改动需同步调整 e2e 预算。
func TestSampleFile_HasExpectedCount(t *testing.T) {
	samples, err := loadRawSamples(repoSamplePath(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(samples) != wantSamples {
		t.Fatalf("len = %d, want %d", len(samples), wantSamples)
	}
}

// TestSampleFile_Covers4Endpoints 锁住"4 类 BAC 场景"覆盖度。
func TestSampleFile_Covers4Endpoints(t *testing.T) {
	samples, err := loadRawSamples(repoSamplePath(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := []string{
		"/api/bac/profile",      // baseline
		"/api/bac/order/7",      // horizontal_priv_esc
		"/api/bac/admin/users",  // vertical_priv_esc
		"/api/bac/admin/delete", // unauthorized_access
	}
	for _, p := range want {
		hit := false
		for _, s := range samples {
			if strings.Contains(s, p) {
				hit = true
				break
			}
		}
		if !hit {
			t.Errorf("缺少 BAC 场景 endpoint: %s", p)
		}
	}
}

// TestSampleFile_AllAdminCookie 锁住"用户正常流量"语义：
// 全部样本都用 admin cookie，e2e-bac 不主动模拟 anonymous 攻击；
// 漏洞由 BAC 子 ReAct fetch_credentials + replay_matrix 内部发现。
func TestSampleFile_AllAdminCookie(t *testing.T) {
	samples, err := loadRawSamples(repoSamplePath(t))
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	for i, s := range samples {
		if !strings.Contains(s, "session=admin_sess_a1b2c3") {
			t.Errorf("[%d] 应使用 admin cookie 模拟正常流量", i)
		}
	}
}

func TestFilterBAC(t *testing.T) {
	tests := []struct {
		name string
		in   []vulnfinding.VulnFinding
		want int
	}{
		{"nil", nil, 0},
		{"纯 BAC", []vulnfinding.VulnFinding{{Kind: "bac.horizontal_priv_esc"}, {Kind: "bac.vertical_priv_esc"}}, 2},
		{"纯非 BAC", []vulnfinding.VulnFinding{{Kind: "leak.api_key"}}, 0},
		{"混合", []vulnfinding.VulnFinding{{Kind: "bac.horizontal_priv_esc"}, {Kind: "leak.api_key"}, {Kind: "bac.vertical_priv_esc"}}, 2},
		{"前缀近似但不匹配", []vulnfinding.VulnFinding{{Kind: "background.scan"}, {Kind: "bac"}}, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := filterByPrefix(tc.in, "bac.")
			if len(got) != tc.want {
				t.Fatalf("len = %d, want %d", len(got), tc.want)
			}
			for _, f := range got {
				if !strings.HasPrefix(f.Kind, "bac.") {
					t.Errorf("非 bac.* kind: %q", f.Kind)
				}
			}
		})
	}
}

func TestCountKinds(t *testing.T) {
	in := []vulnfinding.VulnFinding{
		{Kind: "bac.horizontal_priv_esc"},
		{Kind: "bac.horizontal_priv_esc"},
		{Kind: "bac.vertical_priv_esc"},
		{Kind: "bac.unauthorized_access"},
	}
	got := countKinds(in)
	if len(got) != 3 {
		t.Fatalf("类别数 = %d, want 3", len(got))
	}
	if got["bac.horizontal_priv_esc"] != 2 {
		t.Errorf("horizontal 计数 = %d, want 2", got["bac.horizontal_priv_esc"])
	}
}

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

// repoSamplePath 找到 examples/sample_bac_raw.json：cmd/e2e-bac 跑 go test 时
// 工作目录是该包目录，需向上回到仓库根。
func repoSamplePath(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := filepath.Join(wd, "..", "..")
	return filepath.Join(root, "examples", "sample_bac_raw.json")
}
