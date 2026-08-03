//go:build integration

package seed

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	cfghunter "github.com/V3teran/liusha/internal/config/hunter"
	cfgplaybook "github.com/V3teran/liusha/internal/config/playbook"
	cfgscenario "github.com/V3teran/liusha/internal/config/scenario"
	"github.com/V3teran/liusha/internal/dbtest"
)

// writeSeedTree 在 t.TempDir 下铺一套最小种子：1 编排猎手 + 1 领域猎手、
// 1 swarm 剧本（引用领域猎手）、1 场景（引用剧本）。返回根目录。
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
max_iterations: 80
---
侦察正文
`)
	mustWrite("playbooks/web-pentest.yaml", `code: web-pentest
name: Web 渗透剧本
description: swarm 组合
hunters:
  - reconnaissance
`)
	mustWrite("scenarios/web-pentest.md", `---
id: web-pentest
name: Web 渗透测试
description: 全面 Web 渗透
engine: swarm
playbook: web-pentest
domain: web
---
Web 场景领域侧重正文
`)
	return root
}

// stores 一次建齐三个配置 store。
func stores(t *testing.T) (*cfghunter.Store, *cfgplaybook.Store, *cfgscenario.Store) {
	t.Helper()
	pool := dbtest.NewPgPool(t)
	return cfghunter.NewStore(pool), cfgplaybook.NewStore(pool), cfgscenario.NewStore(pool)
}

// TestImport_FirstFillWiresEverything 验证：空库首次导入后，三类配置齐备且组合/FK 正确。
func TestImport_FirstFillWiresEverything(t *testing.T) {
	ctx := context.Background()
	root := writeSeedTree(t)
	h, p, s := stores(t)

	if err := Import(ctx, root, h, p, s); err != nil {
		t.Fatalf("import: %v", err)
	}

	orch, err := h.GetOrchestrator(ctx)
	if err != nil {
		t.Fatalf("get orchestrator: %v", err)
	}
	if orch.Code != "orchestrator" || orch.MaxIterations != 100 {
		t.Fatalf("编排猎手不匹配: %+v", orch)
	}

	pb, err := p.GetByCode(ctx, "web-pentest")
	if err != nil {
		t.Fatalf("get playbook: %v", err)
	}
	hunters, err := p.ListHunters(ctx, pb.ID)
	if err != nil {
		t.Fatalf("list hunters: %v", err)
	}
	if len(hunters) != 1 || hunters[0].Code != "reconnaissance" {
		t.Fatalf("剧本组合不匹配: %+v", hunters)
	}

	sc, err := s.GetByCode(ctx, "web-pentest")
	if err != nil {
		t.Fatalf("get scenario: %v", err)
	}
	if sc.Engine != cfgscenario.EngineSwarm || sc.PlaybookID != pb.ID || sc.Domain != "web" {
		t.Fatalf("场景不匹配: %+v", sc)
	}
	if sc.Instruction == "" {
		t.Fatal("场景 instruction 应取 md 正文，不应为空")
	}
}

// TestImport_InsertOnlyNeverOverwrites 是核心保证：二次导入不覆盖 DB 里被改过的行。
// 先导一次 → 改某猎手 DB body → 再导一次 → 断言 body 保持被改后的值。
func TestImport_InsertOnlyNeverOverwrites(t *testing.T) {
	ctx := context.Background()
	root := writeSeedTree(t)
	h, p, s := stores(t)

	if err := Import(ctx, root, h, p, s); err != nil {
		t.Fatalf("first import: %v", err)
	}

	// 模拟前端/运维在 DB 里改了猎手 body。
	const mutated = "被运维改过的正文——不该被种子盖回"
	got, err := h.GetByCode(ctx, "reconnaissance")
	if err != nil {
		t.Fatalf("get before update: %v", err)
	}
	if _, err := h.Update(ctx, cfghunter.NewParams{
		Code:          got.Code,
		Kind:          got.Kind,
		Name:          got.Name,
		Description:   got.Description,
		Body:          mutated,
		Tools:         got.Tools,
		MaxIterations: got.MaxIterations,
		Enabled:       got.Enabled,
	}); err != nil {
		t.Fatalf("mutate body: %v", err)
	}

	// 二次导入：insert-only，应整行跳过。
	if err := Import(ctx, root, h, p, s); err != nil {
		t.Fatalf("second import: %v", err)
	}

	after, err := h.GetByCode(ctx, "reconnaissance")
	if err != nil {
		t.Fatalf("get after reimport: %v", err)
	}
	if after.Body != mutated {
		t.Fatalf("insert-only 被破坏：body 被种子覆盖回 %q", after.Body)
	}
}

// TestImport_Idempotent 验证：重复导入不报错、不产生重复行。
func TestImport_Idempotent(t *testing.T) {
	ctx := context.Background()
	root := writeSeedTree(t)
	h, p, s := stores(t)

	for i := 0; i < 3; i++ {
		if err := Import(ctx, root, h, p, s); err != nil {
			t.Fatalf("import #%d: %v", i, err)
		}
	}

	huntersList, err := h.List(ctx, false)
	if err != nil {
		t.Fatalf("list hunters: %v", err)
	}
	if len(huntersList) != 2 {
		t.Fatalf("应恰好 2 个猎手（幂等），得 %d", len(huntersList))
	}
	pbs, err := p.List(ctx)
	if err != nil {
		t.Fatalf("list playbooks: %v", err)
	}
	if len(pbs) != 1 {
		t.Fatalf("应恰好 1 个剧本（幂等），得 %d", len(pbs))
	}
	scs, err := s.List(ctx, false)
	if err != nil {
		t.Fatalf("list scenarios: %v", err)
	}
	if len(scs) != 1 {
		t.Fatalf("应恰好 1 个场景（幂等），得 %d", len(scs))
	}
}
