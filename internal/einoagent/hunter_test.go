package einoagent_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/einoagent"
)

// writeHunter 在 dir 下写一个猎手 md（测试辅助）。
func writeHunter(t *testing.T, dir, file, content string) {
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
  - list_traffic
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

func TestHunterSoloKindExists(t *testing.T) {
	if einoagent.HunterSolo != "solo" {
		t.Fatalf("HunterSolo = %q, want solo", einoagent.HunterSolo)
	}
}

func TestLoadHunters_ParsesAndClassifies(t *testing.T) {
	dir := t.TempDir()
	writeHunter(t, dir, "orchestrator.md", orchestratorMD)
	writeHunter(t, dir, "exploitation.md", exploitationMD)

	hunters, err := einoagent.LoadHunters(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(hunters) != 2 {
		t.Fatalf("应加载 2 个猎手，得到 %d", len(hunters))
	}
	// 字典序：exploitation 在前（e < o）
	if hunters[0].ID != "exploitation" || hunters[1].ID != "orchestrator" {
		t.Errorf("猎手顺序/id 错: %+v", hunters)
	}
	// exploitation 字段
	st := hunters[0]
	if st.Name != "渗透exploitation" || st.Kind != einoagent.HunterSubAgent || st.MaxIterations != 120 {
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
	writeHunter(t, dir, "orchestrator.md", orchestratorMD)
	writeHunter(t, dir, "exploitation.md", exploitationMD)
	hunters, _ := einoagent.LoadHunters(dir)

	orch, err := einoagent.Orchestrator(hunters)
	if err != nil {
		t.Fatal(err)
	}
	if orch.ID != "orchestrator" || orch.Kind != einoagent.HunterOrchestrator {
		t.Errorf("orchestrator 错: %+v", orch)
	}
	subs := einoagent.SubAgents(hunters)
	if len(subs) != 1 || subs[0].ID != "exploitation" {
		t.Errorf("subAgents 错: %+v", subs)
	}
}

func TestLoadHunters_MissingDescription(t *testing.T) {
	dir := t.TempDir()
	writeHunter(t, dir, "bad.md", "---\nid: x\nkind: subagent\n---\nbody\n")
	if _, err := einoagent.LoadHunters(dir); err == nil || !strings.Contains(err.Error(), "description") {
		t.Fatalf("缺 description 应报错，得到: %v", err)
	}
}

func TestLoadHunters_DuplicateID(t *testing.T) {
	dir := t.TempDir()
	writeHunter(t, dir, "a.md", exploitationMD)
	writeHunter(t, dir, "b.md", exploitationMD) // 同 id=exploitation
	if _, err := einoagent.LoadHunters(dir); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("重复 id 应报错，得到: %v", err)
	}
}

func TestLoadHunters_DefaultKindSubAgent(t *testing.T) {
	dir := t.TempDir()
	writeHunter(t, dir, "r.md", "---\nid: recon\ndescription: 侦察\n---\nbody\n")
	hunters, err := einoagent.LoadHunters(dir)
	if err != nil {
		t.Fatal(err)
	}
	if hunters[0].Kind != einoagent.HunterSubAgent {
		t.Errorf("kind 省略应默认 subagent，得到 %q", hunters[0].Kind)
	}
}

func TestLoadHunters_SoloKindAccepted(t *testing.T) {
	dir := t.TempDir()
	writeHunter(t, dir, "s.md", "---\nid: traffic-analysis\nkind: solo\ndescription: 流量分析\n---\nbody\n")
	hunters, err := einoagent.LoadHunters(dir)
	if err != nil {
		t.Fatal(err)
	}
	if hunters[0].Kind != einoagent.HunterSolo {
		t.Errorf("kind=solo 应被接受，得到 %q", hunters[0].Kind)
	}
}

func TestOrchestrator_MissingOrTooMany(t *testing.T) {
	dir := t.TempDir()
	writeHunter(t, dir, "exploitation.md", exploitationMD) // 只有 subagent
	hunters, _ := einoagent.LoadHunters(dir)
	if _, err := einoagent.Orchestrator(hunters); err == nil {
		t.Fatal("无 orchestrator 应报错")
	}
}
