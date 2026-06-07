package einoagent_test

import (
	"path/filepath"
	"testing"

	"github.com/V3teran/liusha/internal/einoagent"
)

// TestShippedRoleFiles 校验仓库实际的 agents/*.md：能加载、恰一个 orchestrator、
// 每个角色声明的工具都在注册表里（防 md 写错工具名 / 漏注册）。
// 路径相对本包目录（internal/einoagent）回到仓库根的 agents/。
func TestShippedRoleFiles(t *testing.T) {
	dir := filepath.Join("..", "..", "agents")
	roles, err := einoagent.LoadRoles(dir)
	if err != nil {
		t.Fatalf("加载 %s 失败: %v", dir, err)
	}
	if len(roles) == 0 {
		t.Fatal("agents/ 没加载到任何角色")
	}

	// 恰一个 orchestrator（commander）。
	orch, err := einoagent.Orchestrator(roles)
	if err != nil {
		t.Fatalf("Orchestrator: %v", err)
	}
	if orch.ID != "commander" {
		t.Errorf("orchestrator 应为 commander，得 %q", orch.ID)
	}

	// 至少一个子代理。
	if subs := einoagent.SubAgents(roles); len(subs) == 0 {
		t.Error("无子代理（striker）")
	}

	// 所有角色的工具名都必须在注册表里。
	known := map[string]bool{}
	for _, n := range einoagent.KnownToolNames() {
		known[n] = true
	}
	for _, r := range roles {
		for _, tn := range r.Tools {
			if !known[tn] {
				t.Errorf("角色 %q 声明了未注册工具 %q", r.ID, tn)
			}
		}
	}
}
