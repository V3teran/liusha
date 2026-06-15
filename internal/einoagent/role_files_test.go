package einoagent_test

import (
	"path/filepath"
	"testing"

	"github.com/V3teran/liusha/internal/einoagent"
)

// TestShippedRoleFiles 校验仓库实际的 hunters/{active,passive}/*.md：
//   - active 子目录能加载、恰一个 orchestrator、含 reconnaissance + exploitation
//   - passive 子目录含 traffic-analysis
//   - 每个角色声明的工具都在注册表里（防 md 写错工具名 / 漏注册）
// 子目录隔离：active 的 LoadRoles 不应扫到 passive 角色（traffic-analysis 不进 swarm）。
func TestShippedRoleFiles(t *testing.T) {
	known := map[string]bool{}
	for _, n := range einoagent.KnownToolNames() {
		known[n] = true
	}
	assertToolsKnown := func(roles []einoagent.RoleDef) {
		for _, r := range roles {
			for _, tn := range r.Tools {
				if !known[tn] {
					t.Errorf("角色 %q 声明了未注册工具 %q", r.ID, tn)
				}
			}
		}
	}

	// --- active 子目录（deep swarm）---
	activeDir := filepath.Join("..", "..", "hunters", "active")
	active, err := einoagent.LoadRoles(activeDir)
	if err != nil {
		t.Fatalf("加载 %s 失败: %v", activeDir, err)
	}
	if len(active) == 0 {
		t.Fatal("hunters/active/ 没加载到任何角色")
	}
	orch, err := einoagent.Orchestrator(active)
	if err != nil {
		t.Fatalf("Orchestrator: %v", err)
	}
	if orch.ID != "orchestrator" {
		t.Errorf("orchestrator 应为 orchestrator，得 %q", orch.ID)
	}
	ids := map[string]bool{}
	for _, r := range active {
		ids[r.ID] = true
	}
	for _, want := range []string{"reconnaissance", "exploitation"} {
		if !ids[want] {
			t.Errorf("active 缺子代理 %q", want)
		}
	}
	if ids["traffic-analysis"] {
		t.Error("traffic-analysis 不应出现在 active 目录（应在 hunters/passive/，否则会被 deep swarm 误派）")
	}
	assertToolsKnown(active)

	// --- passive 子目录（单 agent）---
	passiveDir := filepath.Join("..", "..", "hunters", "passive")
	passive, err := einoagent.LoadRoles(passiveDir)
	if err != nil {
		t.Fatalf("加载 %s 失败: %v", passiveDir, err)
	}
	pids := map[string]bool{}
	for _, r := range passive {
		pids[r.ID] = true
	}
	if !pids["traffic-analysis"] {
		t.Error("hunters/passive/ 缺 traffic-analysis")
	}
	assertToolsKnown(passive)
}
