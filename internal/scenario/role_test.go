package scenario_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/V3teran/liusha/internal/scenario"
)

func writeRole(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRoles_ParsesAndSorts(t *testing.T) {
	dir := t.TempDir()
	writeRole(t, dir, "web.md", `---
id: web-pentest
name: Web 渗透
description: web 渗透场景
mode: active
---
你是 web 渗透专家`)
	writeRole(t, dir, "passive.md", `---
id: passive-recon
name: 被动侦察
description: 被动流量分析
mode: passive
---
逐条分析流量`)

	roles, err := scenario.LoadRoles(dir)
	if err != nil {
		t.Fatalf("LoadRoles: %v", err)
	}
	if len(roles) != 2 {
		t.Fatalf("应 2 个 role，得 %d", len(roles))
	}
	// 字典序：passive-recon < web-pentest
	if roles[0].ID != "passive-recon" || roles[1].ID != "web-pentest" {
		t.Errorf("排序错: %s, %s", roles[0].ID, roles[1].ID)
	}
	if roles[1].Mode != scenario.ModeActive || roles[1].SystemPrompt != "你是 web 渗透专家" {
		t.Errorf("web role 解析错: mode=%s prompt=%q", roles[1].Mode, roles[1].SystemPrompt)
	}
}

func TestLoadRoles_Validation(t *testing.T) {
	cases := map[string]string{
		"缺 description": "---\nid: x\nmode: active\n---\nbody",
		"mode 非法":       "---\nid: x\ndescription: d\nmode: bogus\n---\nbody",
		"缺 id":          "---\nname: n\ndescription: d\nmode: active\n---\nbody",
		"缺 frontmatter": "没有 frontmatter 的纯正文",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			writeRole(t, dir, "bad.md", content)
			if _, err := scenario.LoadRoles(dir); err == nil {
				t.Errorf("%s 应报错", name)
			}
		})
	}
}

func TestLoadRoles_DuplicateID(t *testing.T) {
	dir := t.TempDir()
	writeRole(t, dir, "a.md", "---\nid: dup\ndescription: d\nmode: active\n---\nA")
	writeRole(t, dir, "b.md", "---\nid: dup\ndescription: d\nmode: active\n---\nB")
	if _, err := scenario.LoadRoles(dir); err == nil {
		t.Error("重复 id 应报错")
	}
}

func TestByIDAndDefault(t *testing.T) {
	roles := []scenario.Role{
		{ID: "web-pentest", Mode: scenario.ModeActive},
		{ID: "passive-recon", Mode: scenario.ModePassive},
	}
	if r, ok := scenario.ByID(roles, "web-pentest"); !ok || r.Mode != scenario.ModeActive {
		t.Error("ByID web-pentest 失败")
	}
	if _, ok := scenario.ByID(roles, "nope"); ok {
		t.Error("ByID 未知 id 应 false")
	}
	if r, ok := scenario.DefaultForMode(roles, scenario.ModePassive); !ok || r.ID != "passive-recon" {
		t.Error("DefaultForMode passive 失败")
	}
}

// TestShippedRoleFiles 校验仓库实际的 roles/*.md：能加载、含 web-pentest(active) + passive-recon(passive)。
func TestShippedRoleFiles(t *testing.T) {
	roles, err := scenario.LoadRoles(filepath.Join("..", "..", "scenarios"))
	if err != nil {
		t.Fatalf("加载 scenarios/: %v", err)
	}
	web, ok := scenario.ByID(roles, "web-pentest")
	if !ok || web.Mode != scenario.ModeActive {
		t.Error("缺 web-pentest(active)")
	}
	pas, ok := scenario.ByID(roles, "passive-recon")
	if !ok || pas.Mode != scenario.ModePassive {
		t.Error("缺 passive-recon(passive)")
	}
	if len(scenario.FilterByMode(roles, scenario.ModeActive)) == 0 {
		t.Error("无 active role")
	}
}
