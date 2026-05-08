package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/finding"
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
// 全部样本都用 admin cookie，触发器不主动模拟 anonymous 攻击；
// 漏洞由 BAC 子 ReAct fetch_credentials + run_replay 内部发现。
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
		in   []finding.VulnFinding
		want int
	}{
		{"nil", nil, 0},
		{"纯 BAC", []finding.VulnFinding{{Kind: "bac.horizontal_priv_esc"}, {Kind: "bac.vertical_priv_esc"}}, 2},
		{"纯非 BAC", []finding.VulnFinding{{Kind: "leak.api_key"}}, 0},
		{"混合", []finding.VulnFinding{{Kind: "bac.horizontal_priv_esc"}, {Kind: "leak.api_key"}, {Kind: "bac.vertical_priv_esc"}}, 2},
		{"前缀近似但不匹配", []finding.VulnFinding{{Kind: "background.scan"}, {Kind: "bac"}}, 0},
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
	in := []finding.VulnFinding{
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
	const k = "LIUSHA_E2E_TEST_KEY"
	t.Setenv(k, "")
	if got := envOr(k, "default"); got != "default" {
		t.Errorf("envOr(空) = %q, want default", got)
	}
	t.Setenv(k, "override")
	if got := envOr(k, "default"); got != "override" {
		t.Errorf("envOr(set) = %q, want override", got)
	}
}

// TestSelectProfiles 锁住 CLI args 解析行为：空 = 全部、多选、去重、未知报错。
func TestSelectProfiles(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		want    []string // 期望的 profile 名（按返回顺序）
		wantErr bool
	}{
		{"空 args 返回全部 profile（字典序）", nil, []string{"bac", "sqli"}, false},
		{"单选 bac", []string{"bac"}, []string{"bac"}, false},
		{"单选 sqli", []string{"sqli"}, []string{"sqli"}, false},
		{"多选保留输入顺序", []string{"sqli", "bac"}, []string{"sqli", "bac"}, false},
		{"重复参数自动去重", []string{"bac", "bac", "sqli"}, []string{"bac", "sqli"}, false},
		{"大小写/空格不敏感", []string{" BAC ", "Sqli"}, []string{"bac", "sqli"}, false},
		{"未知 profile 报错", []string{"xxx"}, nil, true},
		{"混入未知则整体失败", []string{"bac", "xxx"}, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := selectProfiles(tc.args)
			if tc.wantErr {
				if err == nil {
					t.Fatal("期望 err，得到 nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("意外错误: %v", err)
			}
			gotNames := make([]string, len(got))
			for i, p := range got {
				gotNames[i] = p.name
			}
			if strings.Join(gotNames, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("got=%v want=%v", gotNames, tc.want)
			}
		})
	}
}

// repoSamplePath 找到 examples/sample_bac_raw.json：cmd/e2e 跑 go test 时
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
