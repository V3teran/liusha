//go:build integration

package planner_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/db"
	"github.com/V3teran/liusha/internal/planner"
	"github.com/V3teran/liusha/internal/worldmodel"
)

// L4 契约缝：Planner 对真实 worldmodel.Store 跑规划环。
// 建一张真攻击图（Target→Asset 已探、Credential 未兑现、Access 新立足点），
// 验证 Plan 读真库 → 推导 frontier → 排序，结论与图状态一致。
// 需 LIUSHA_POSTGRES_DSN；未设则 skip。
func TestPlanner_PlanAgainstRealStore(t *testing.T) {
	dsn := os.Getenv("LIUSHA_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("LIUSHA_POSTGRES_DSN 未设，跳过 planner 集成测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := db.NewPgPool(ctx, dsn, 5, 1, 5, 0)
	if err != nil {
		t.Fatalf("连库: %v", err)
	}
	defer pool.Close()
	store := worldmodel.NewStore(pool)

	const taskID = "planner-itest-plan" // = assignment_id 语义
	defer func() {
		_, _ = pool.Exec(ctx, `DELETE FROM wm_edge WHERE task_id=$1`, taskID)
		_, _ = pool.Exec(ctx, `DELETE FROM wm_node WHERE task_id=$1`, taskID)
	}()
	// 前置清场，避免上次残留污染。
	_, _ = pool.Exec(ctx, `DELETE FROM wm_edge WHERE task_id=$1`, taskID)
	_, _ = pool.Exec(ctx, `DELETE FROM wm_node WHERE task_id=$1`, taskID)

	ref := func(loc string) worldmodel.TargetRef {
		return worldmodel.TargetRef{Domain: "web", RefKind: "host", Locator: loc}
	}
	mk := func(kind worldmodel.NodeKind, loc string) worldmodel.Node {
		n, e := store.UpsertNode(ctx, worldmodel.Node{TaskID: taskID, Kind: kind, Ref: ref(loc)})
		if e != nil {
			t.Fatalf("落节点 %s/%s: %v", kind, loc, e)
		}
		return n
	}

	// 建图：
	//   t1(Target) --on-- a1(Asset)      Target 已探出面 → t1 不再 recon；a1 无 Finding → exploit
	//   c1(Credential)  未兑现            → use-credential
	//   ac1(Access)     裸立足点          → post-exploit
	t1 := mk(worldmodel.KindTarget, "app.example.com")
	a1 := mk(worldmodel.KindAsset, "app.example.com/api")
	_ = mk(worldmodel.KindCredential, "admin@app.example.com")
	_ = mk(worldmodel.KindAccess, "shell://app.example.com")

	if err := store.LinkEdge(ctx, worldmodel.Edge{
		TaskID: taskID, Rel: worldmodel.RelOn, Src: a1.ID, Dst: t1.ID,
	}); err != nil {
		t.Fatalf("连 Asset on Target: %v", err)
	}

	p := planner.New(store, nil)
	intents, err := p.Plan(ctx, taskID)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}

	// 期望 3 条：exploit(a1)、use-credential(c1)、post-exploit(ac1)；t1 已展开不出。
	byKind := map[planner.MoveKind]int{}
	for _, in := range intents {
		byKind[in.Kind]++
	}
	if got := len(intents); got != 3 {
		t.Fatalf("应产出 3 条招法, got %d: %+v", got, intents)
	}
	if byKind[planner.MoveEnumerate] != 0 {
		t.Errorf("已探面的 Target 不应产出 recon: %+v", intents)
	}
	for _, k := range []planner.MoveKind{
		planner.MoveExploit, planner.MoveEscalate, planner.MovePersist,
	} {
		if byKind[k] != 1 {
			t.Errorf("应恰有 1 条 %s, got %d", k, byKind[k])
		}
	}

	// 排序：纵深优先，use-credential 必居首。
	if intents[0].Kind != planner.MoveEscalate {
		t.Errorf("首位应为 use-credential, got %s（完整 %+v）", intents[0].Kind, intents)
	}

	// frontier 耗尽验证：给 a1 挂 Finding、c1 换出 Access、ac1 探出 Asset 后，
	// 仅剩最外层无法自动闭合的项——这里只验 exploit 缺口可闭合。
	f1 := mk(worldmodel.KindFinding, "app.example.com/api#sqli")
	if err := store.LinkEdge(ctx, worldmodel.Edge{
		TaskID: taskID, Rel: worldmodel.RelOn, Src: f1.ID, Dst: a1.ID,
	}); err != nil {
		t.Fatalf("连 Finding on Asset: %v", err)
	}
	intents2, err := p.Plan(ctx, taskID)
	if err != nil {
		t.Fatalf("Plan(2): %v", err)
	}
	for _, in := range intents2 {
		if in.Kind == planner.MoveExploit {
			t.Errorf("a1 已挂 Finding，不应再 exploit: %+v", intents2)
		}
	}
}
