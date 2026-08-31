//go:build integration

package seed

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cfgagent "github.com/V3teran/liusha/internal/config/agent"
	"github.com/V3teran/liusha/internal/dbtest"
)

// writeSeedTree 在 t.TempDir 下铺一套最小种子：1 编排操作员 + 1 领域操作员、
// 1 swarm 场景（无需枚举子代理）+ 1 solo 场景（引用领域操作员）。返回根目录。
func writeSeedTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	mustWrite := func(rel, content string) {
		abs := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	mustWrite("agents/planner.md", `---
id: planner
name: 编排者
description: 扫描编排者
kind: planner
function_tools:
  - read_findings
max_iterations: 100
---
编排者正文
`)
	mustWrite("agents/reconnaissance.md", `---
id: reconnaissance
name: 侦察
description: 侦察摸底
kind: domain
function_tools:
  - run_command
cli_tools:
  - nmap
max_iterations: 80
---
侦察正文
`)
	mustWrite("scenarios/web-pentest.md", `---
id: web-pentest
name: Web 渗透测试
description: 全面 Web 渗透
engine: swarm
---
Web 场景领域侧重正文
`)
	mustWrite("scenarios/api-pentest.md", `---
id: api-pentest
name: API 渗透
description: 单操作员 HTTP 数据包漏洞测试
engine: solo
solo_agent: reconnaissance
---
API 渗透领域侧重正文
`)
	return root
}

// stores 一次建齐两个配置 store（playbook 层已废）。
	t.Helper()
	pool := dbtest.NewPgPool(t)
}

// TestImport_AgentsAndScenarios 覆盖新两 store 模型：操作员（含 cli_tools）+ 场景
// （swarm 无 solo_agent，solo 解析 solo_agent code→id）。
func TestImport_AgentsAndScenarios(t *testing.T) {
	ctx := context.Background()
	h, s := stores(t)
	root := writeSeedTree(t)

	if err := Import(ctx, root, h, s); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// 操作员：编排者 + 领域操作员，cli_tools 落库
	orch, err := h.GetByCode(ctx, "planner")
	if err != nil {
		t.Fatalf("GetByCode planner: %v", err)
	}
	if orch.Kind != cfgagent.KindPlanner {
		t.Fatalf("planner kind = %q, want planner", orch.Kind)
	}
	recon, err := h.GetByCode(ctx, "reconnaissance")
	if err != nil {
		t.Fatalf("GetByCode reconnaissance: %v", err)
	}
	if recon.Kind != cfgagent.KindExecutor {
		t.Fatalf("reconnaissance kind = %q, want domain", recon.Kind)
	}
	if len(recon.CliTools) != 1 || recon.CliTools[0] != "nmap" {
		t.Fatalf("reconnaissance cli_tools = %v, want [nmap]", recon.CliTools)
	}

	// swarm 场景：无 SoloExecutorID
	web, err := s.GetByCode(ctx, "web-pentest")
	if err != nil {
		t.Fatalf("GetByCode web-pentest: %v", err)
	}
		t.Fatalf("web-pentest engine = %q, want swarm", web.Engine)
	}
	if web.SoloExecutorID != nil {
		t.Fatalf("swarm scenario SoloExecutorID = %v, want nil", *web.SoloExecutorID)
	}

	// solo 场景：SoloExecutorID 解析到领域操作员 id
	apiScen, err := s.GetByCode(ctx, "api-pentest")
	if err != nil {
		t.Fatalf("GetByCode api-pentest: %v", err)
	}
		t.Fatalf("api-pentest engine = %q, want solo", apiScen.Engine)
	}
	if apiScen.SoloExecutorID == nil {
		t.Fatal("solo scenario SoloExecutorID = nil, want reconnaissance id")
	}
	if *apiScen.SoloExecutorID != recon.ID {
		t.Fatalf("api-pentest SoloExecutorID = %q, want %q", *apiScen.SoloExecutorID, recon.ID)
	}
}

// TestImport_Idempotent 二次 Import 不应报错（insert-only：已存在按 code 跳过）。
func TestImport_Idempotent(t *testing.T) {
	ctx := context.Background()
	h, s := stores(t)
	root := writeSeedTree(t)

	if err := Import(ctx, root, h, s); err != nil {
		t.Fatalf("Import #1: %v", err)
	}
	if err := Import(ctx, root, h, s); err != nil {
		t.Fatalf("Import #2 (idempotent): %v", err)
	}

	executors, err := h.List(ctx, false)
	if err != nil {
		t.Fatalf("List executors: %v", err)
	}
	if len(executors) != 2 {
		t.Fatalf("executors count = %d, want 2 (no duplicates)", len(executors))
	}
}
