package planner

import (
	"context"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// fakeWorld 是 worldReader 的内存桩，喂固定图快照。
type fakeWorld struct {
	nodes    []worldmodel.Node
	edges    []worldmodel.Edge
	nodesErr error
	edgesErr error
}

func (f fakeWorld) ListNodes(context.Context, string) ([]worldmodel.Node, error) {
	return f.nodes, f.nodesErr
}
func (f fakeWorld) ListEdges(context.Context, string) ([]worldmodel.Edge, error) {
	return f.edges, f.edgesErr
}
func (f fakeWorld) ListMoves(context.Context, string) ([]worldmodel.Move, error) {
	return nil, nil
}

func TestKillChainStrategy_RanksByDepth(t *testing.T) {
	in := []Move{
		{Kind: MoveEnumerate},
		{Kind: MoveEscalate},
		{Kind: MoveExploit},
		{Kind: MovePersist},
	}
	got, err := NewKillChainStrategy().Rank(context.Background(), RankRequest{Frontier: in})
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	want := []MoveKind{MoveEscalate, MovePersist, MoveExploit, MoveEnumerate}
	for i, k := range want {
		if got[i].Kind != k {
			t.Fatalf("第 %d 位应为 %s，得到 %s（完整 %+v）", i, k, got[i].Kind, got)
		}
	}
}

func TestKillChainStrategy_DoesNotMutateInput(t *testing.T) {
	in := []Move{{Kind: MoveEnumerate}, {Kind: MoveEscalate}}
	_, err := NewKillChainStrategy().Rank(context.Background(), RankRequest{Frontier: in})
	if err != nil {
		t.Fatalf("Rank() error = %v", err)
	}
	if in[0].Kind != MoveEnumerate || in[0].Priority != 0 {
		t.Fatalf("Rank 不应改动入参，得到 %+v", in)
	}
}

func TestPlan_EmptyTaskID_Errors(t *testing.T) {
	p := New(fakeWorld{}, nil)
	if _, err := p.Plan(context.Background(), ""); err == nil {
		t.Fatal("空 taskID 应报错")
	}
}

func TestPlan_PropagatesReadError(t *testing.T) {
	p := New(fakeWorld{nodesErr: errors.New("boom")}, nil)
	if _, err := p.Plan(context.Background(), "s1"); err == nil {
		t.Fatal("读节点失败应上抛")
	}
}

func TestPlan_EndToEnd_ProducesRankedMoves(t *testing.T) {
	// 图：Target(已探出 Asset) + 裸 Credential。
	// 期望 frontier：Asset→exploit、Credential→use-credential；且 use-credential 排前。
	world := fakeWorld{
		nodes: []worldmodel.Node{
			node("t1", worldmodel.KindTarget),
			node("a1", worldmodel.KindAsset),
			node("c1", worldmodel.KindCredential),
		},
		edges: []worldmodel.Edge{edge(worldmodel.RelOn, "a1", "t1")},
	}
	p := New(world, nil)
	got, err := p.Plan(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("应产出 2 条招法，得到 %d: %+v", len(got), got)
	}
	if got[0].Kind != MoveEscalate {
		t.Fatalf("首位应为 use-credential（纵深优先），得到 %s", got[0].Kind)
	}
}

func TestPlan_ExhaustedGraph_EmptyPlan(t *testing.T) {
	// 全覆盖图：Target 有 Asset，Asset 有 Finding —— 无 frontier。
	world := fakeWorld{
		nodes: []worldmodel.Node{
			node("t1", worldmodel.KindTarget),
			node("a1", worldmodel.KindAsset),
			node("f1", worldmodel.KindFinding),
		},
		edges: []worldmodel.Edge{
			edge(worldmodel.RelOn, "a1", "t1"),
			edge(worldmodel.RelOn, "f1", "a1"),
		},
	}
	got, err := New(world, nil).Plan(context.Background(), "s1")
	if err != nil {
		t.Fatalf("Plan 失败: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("耗尽图应产出空计划，得到 %+v", got)
	}
}
