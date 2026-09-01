//go:build integration

package worldmodel_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/executor"
github.com/V3teran/liusha/internal/executor
	"github.com/V3teran/liusha/internal/worldmodel"
)

// A2 契约缝：Registry.Onboard(brief) → TargetRef → UpsertNode(KindObjective, task_id=assignmentID)。
// 验证真实数据通路——brief 里的目标能落成世界模型 KindObjective 节点，多目标全落、幂等不重复。
// task_id 用 assignment 语义（一次交战一个图）。需 LIUSHA_POSTGRES_DSN；未设则 skip。
func TestOnboard_LandsTargetNodes(t *testing.T) {
	dsn := os.Getenv("LIUSHA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LIUSHA_POSTGRES_DSN 未设，跳过 onboard 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPgPool(ctx, dsn, 5, 1, 5, 0)
	if err != nil {
		t.Fatalf("连库: %v", err)
	}
	defer pool.Close()
	store := worldmodel.NewStore(pool)

	const taskID = "wm-itest-onboard" // = assignment_id 语义
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM wm_node WHERE task_id=$1`, taskID)
	}()

	// L2：注册 web Profile，从 brief 解析目标（含端口保真、多目标）。
	reg := executor.NewRegistry()
	reg.Register(executor.New())
	refs, ok := reg.Onboard(ctx, executor.BriefInput{
		Brief: "渗透 https://api.foo.com:8443/login 与 http://bar.local 两个站点",
	})
	if !ok || len(refs) != 2 {
		t.Fatalf("onboard 应解析出 2 个目标, ok=%v refs=%+v", ok, refs)
	}

	// L3：把目标落成 KindObjective 节点（模拟 handler.onboard 的副作用）。
	for _, ref := range refs {
		if _, err := store.UpsertNode(ctx, worldmodel.Node{
			TaskID: taskID,
			Kind:   worldmodel.KindObjective,
			Ref:    ref,
		}); err != nil {
			t.Fatalf("落 KindObjective(%s): %v", ref.Locator, err)
		}
	}

	// 幂等：同 brief 再 onboard 一遍，节点数不增。
	for _, ref := range refs {
		if _, err := store.UpsertNode(ctx, worldmodel.Node{
			TaskID: taskID, Kind: worldmodel.KindObjective, Ref: ref,
		}); err != nil {
			t.Fatalf("幂等 upsert(%s): %v", ref.Locator, err)
		}
	}

	nodes, err := store.ListNodes(ctx, taskID)
	if err != nil {
		t.Fatalf("ListNodes: %v", err)
	}
	if len(nodes) != 2 {
		t.Fatalf("幂等后应为 2 个 KindObjective 节点, got %d", len(nodes))
	}
	for _, n := range nodes {
		if n.Kind != worldmodel.KindObjective {
			t.Errorf("节点 kind 应 objective, got %s", n.Kind)
		}
		if n.Ref.Domain != "web" || n.Ref.RefKind != "host" {
			t.Errorf("节点 ref 域/种类错: %+v", n.Ref)
		}
		// Objective 节点不使用 Confidence 字段（Confidence 只适用于 Observation/Discovery）
		if n.Confidence != nil {
			t.Errorf("objective 节点不应有 confidence 字段, got %v", *n.Confidence)
		}
	}
	// 逐字保真：含端口的目标 locator 带端口。
	var hasPortTarget bool
	for _, n := range nodes {
		if n.Ref.Locator == "api.foo.com:8443" {
			hasPortTarget = true
		}
	}
	if !hasPortTarget {
		t.Errorf("含端口目标 api.foo.com:8443 未落节点, nodes=%+v", nodes)
	}
}
