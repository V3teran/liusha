//go:build integration

package seed

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	"github.com/V3teran/liusha/internal/dbtest"
)

// writeSeedTree 在 t.TempDir 下铺一套最小种子：1 编排猎手 + 1 领域猎手、
// 1 swarm 场景（无需枚举子代理）+ 1 solo 场景（引用领域猎手）。返回根目录。
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

	mustWrite("hunters/orchestrator.md", `---
id: orchestrator
name: 编排者
description: 扫描编排者
kind: orchestrator
tools:
  - read_findings
max_iterations: 100
---
编排者正文
`)
	mustWrite("hunters/reconnaissance.md", `---
id: reconnaissance
name: 侦察
description: 侦察摸底
kind: domain
tools:
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
domain: web
---
Web 场景领域侧重正文
`)
	mustWrite("scenarios/passive-recon.md", `---
id: passive-recon
name: 被动侦察
description: 单猎手流量分析
engine: solo
solo_hunter: reconnaissance
domain: web
---
被动侦察领域侧重正文
`)
	return root
}

// stores 一次建齐两个配置 store（playbook 层已废）。
func stores(t *testing.T) (*cfghunter.Store, *cfgscenario.Store) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	return cfghunter.NewStore(pool), cfgscenario.NewStore(pool)
}

// TestImport_HuntersAndScenarios 覆盖新两 store 模型：猎手（含 cli_tools）+ 场景
// （swarm 无 solo_hunter，solo 解析 solo_hunter code→id）。
func TestImport_HuntersAndScenarios(t *testing.T) {
	ctx := context.Background()
	h, s := stores(t)
	root := writeSeedTree(t)

	if err := Import(ctx, root, h, s); err != nil {
		t.Fatalf("Import: %v", err)
	}

	// 猎手：编排者 + 领域猎手，cli_tools 落库
	orch, err := h.GetByCode(ctx, "orchestrator")
	if err != nil {
		t.Fatalf("GetByCode orchestrator: %v", err)
	}
	if orch.Kind != cfghunter.KindOrchestrator {
		t.Fatalf("orchestrator kind = %q, want orchestrator", orch.Kind)
	}
	recon, err := h.GetByCode(ctx, "reconnaissance")
	if err != nil {
		t.Fatalf("GetByCode reconnaissance: %v", err)
	}
	if recon.Kind != cfghunter.KindDomain {
		t.Fatalf("reconnaissance kind = %q, want domain", recon.Kind)
	}
	if len(recon.CliTools) != 1 || recon.CliTools[0] != "nmap" {
		t.Fatalf("reconnaissance cli_tools = %v, want [nmap]", recon.CliTools)
	}

	// swarm 场景：无 SoloHunterID
	web, err := s.GetByCode(ctx, "web-pentest")
	if err != nil {
		t.Fatalf("GetByCode web-pentest: %v", err)
	}
	if web.Engine != cfgscenario.EngineSwarm {
		t.Fatalf("web-pentest engine = %q, want swarm", web.Engine)
	}
	if web.SoloHunterID != nil {
		t.Fatalf("swarm scenario SoloHunterID = %v, want nil", *web.SoloHunterID)
	}

	// solo 场景：SoloHunterID 解析到领域猎手 id
	passive, err := s.GetByCode(ctx, "passive-recon")
	if err != nil {
		t.Fatalf("GetByCode passive-recon: %v", err)
	}
	if passive.Engine != cfgscenario.EngineSolo {
		t.Fatalf("passive-recon engine = %q, want solo", passive.Engine)
	}
	if passive.SoloHunterID == nil {
		t.Fatal("solo scenario SoloHunterID = nil, want reconnaissance id")
	}
	if *passive.SoloHunterID != recon.ID {
		t.Fatalf("passive-recon SoloHunterID = %q, want %q", *passive.SoloHunterID, recon.ID)
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

	hunters, err := h.List(ctx, false)
	if err != nil {
		t.Fatalf("List hunters: %v", err)
	}
	if len(hunters) != 2 {
		t.Fatalf("hunters count = %d, want 2 (no duplicates)", len(hunters))
	}
}
