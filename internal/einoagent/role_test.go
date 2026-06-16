package einoagent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/einoagent"
)

// writeRole 在 dir 下写一个角色 md（测试辅助）。
func writeRole(t *testing.T, dir, file, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const orchestratorMD = `---
id: orchestrator
name: 渗透编排者
kind: orchestrator
description: 拆活派 exploitation，不亲自挖洞
max_iterations: 300
tools:
  - read_findings
  - list_flows
---
你是渗透编排者。
`

const exploitationMD = `---
id: exploitation
name: 渗透exploitation
kind: subagent
description: 接 brief 深挖单个攻击面并 write_finding
max_iterations: 120
tools:
  - read_findings
  - write_finding
  - run_command
  - done
---
你是渗透exploitation，根据 brief 深挖。
`

func TestLoadRoles_ParsesAndClassifies(t *testing.T) {
	dir := t.TempDir()
	writeRole(t, dir, "orchestrator.md", orchestratorMD)
	writeRole(t, dir, "exploitation.md", exploitationMD)

	roles, err := einoagent.LoadRoles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 2 {
		t.Fatalf("应加载 2 个角色，得到 %d", len(roles))
	}
	// 字典序：exploitation 在前（e < o）
	if roles[0].ID != "exploitation" || roles[1].ID != "orchestrator" {
		t.Errorf("角色顺序/id 错: %+v", roles)
	}
	// exploitation 字段
	st := roles[0]
	if st.Name != "渗透exploitation" || st.Kind != einoagent.RoleSubAgent || st.MaxIterations != 120 {
		t.Errorf("exploitation 字段错: %+v", st)
	}
	if len(st.Tools) != 4 || st.Tools[1] != "write_finding" {
		t.Errorf("exploitation tools 错: %v", st.Tools)
	}
	if !strings.Contains(st.SystemPrompt, "深挖") {
		t.Errorf("exploitation body 错: %q", st.SystemPrompt)
	}
}

func TestOrchestrator_And_SubAgents(t *testing.T) {
	dir := t.TempDir()
	writeRole(t, dir, "orchestrator.md", orchestratorMD)
	writeRole(t, dir, "exploitation.md", exploitationMD)
	roles, _ := einoagent.LoadRoles(dir)

	orch, err := einoagent.Orchestrator(roles)
	if err != nil {
		t.Fatal(err)
	}
	if orch.ID != "orchestrator" || orch.Kind != einoagent.RoleOrchestrator {
		t.Errorf("orchestrator 错: %+v", orch)
	}
	subs := einoagent.SubAgents(roles)
	if len(subs) != 1 || subs[0].ID != "exploitation" {
		t.Errorf("subAgents 错: %+v", subs)
	}
}

func TestLoadRoles_MissingDescription(t *testing.T) {
	dir := t.TempDir()
	writeRole(t, dir, "bad.md", "---\nid: x\nkind: subagent\n---\nbody\n")
	if _, err := einoagent.LoadRoles(dir); err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("缺 description 应报错，得到: %v", err)
	}
}

func TestLoadRoles_DuplicateID(t *testing.T) {
	dir := t.TempDir()
	writeRole(t, dir, "a.md", exploitationMD)
	writeRole(t, dir, "b.md", exploitationMD) // 同 id=exploitation
	if _, err := einoagent.LoadRoles(dir); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复 id 应报错，得到: %v", err)
	}
}

func TestLoadRoles_DefaultKindSubAgent(t *testing.T) {
	dir := t.TempDir()
	writeRole(t, dir, "r.md", "---\nid: recon\ndescription: 侦察\n---\nbody\n")
	roles, err := einoagent.LoadRoles(dir)
	if err != nil {
		t.Fatal(err)
	}
	if roles[0].Kind != einoagent.RoleSubAgent {
		t.Errorf("kind 省略应默认 subagent，得到 %q", roles[0].Kind)
	}
}

func TestOrchestrator_MissingOrTooMany(t *testing.T) {
	dir := t.TempDir()
	writeRole(t, dir, "exploitation.md", exploitationMD) // 只有 subagent
	roles, _ := einoagent.LoadRoles(dir)
	if _, err := einoagent.Orchestrator(roles); err == nil {
		t.Fatal("无 orchestrator 应报错")
	}
}
