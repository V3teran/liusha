package planner

import (
	"testing"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// node/edge 构造助手：让用例聚焦图形状，不淹没在字面量里。
func node(id string, kind worldmodel.NodeKind) worldmodel.Node {
	return worldmodel.Node{ID: id, Kind: kind, Ref: worldmodel.TargetRef{Locator: id}}
}

func edge(rel worldmodel.EdgeRel, src, dst string) worldmodel.Edge {
	return worldmodel.Edge{Rel: rel, Src: src, Dst: dst}
}

// 收集 frontier 里某类招法锚定的节点 ID，便于断言。
func onNodesOf(moves []Move, kind MoveKind) map[string]bool {
	got := map[string]bool{}
	for _, in := range moves {
		if in.Kind == kind {
			got[in.OnNodeID] = true
		}
	}
	return got
}

func TestDeriveFrontier_TargetWithoutAsset_YieldsRecon(t *testing.T) {
	nodes := []worldmodel.Node{node("t1", worldmodel.KindTarget)}
	f := deriveFrontier(nodes, nil)
	if got := onNodesOf(f, MoveEnumerate); !got["t1"] {
		t.Fatalf("裸 Target 应产出 recon，得到 %+v", f)
	}
}

func TestDeriveFrontier_TargetWithAsset_NoRecon(t *testing.T) {
	nodes := []worldmodel.Node{
		node("t1", worldmodel.KindTarget),
		node("a1", worldmodel.KindAsset),
	}
	edges := []worldmodel.Edge{edge(worldmodel.RelOn, "a1", "t1")} // Asset on Target
	f := deriveFrontier(nodes, edges)
	if got := onNodesOf(f, MoveEnumerate); got["t1"] {
		t.Fatalf("已下挂 Asset 的 Target 不应再 recon，得到 %+v", f)
	}
}

func TestDeriveFrontier_AssetWithoutFinding_YieldsExploit(t *testing.T) {
	nodes := []worldmodel.Node{node("a1", worldmodel.KindAsset)}
	f := deriveFrontier(nodes, nil)
	if got := onNodesOf(f, MoveExploit); !got["a1"] {
		t.Fatalf("无 Finding 的 Asset 应产出 exploit，得到 %+v", f)
	}
}

func TestDeriveFrontier_AssetWithFinding_NoExploit(t *testing.T) {
	nodes := []worldmodel.Node{
		node("a1", worldmodel.KindAsset),
		node("f1", worldmodel.KindFinding),
	}
	edges := []worldmodel.Edge{edge(worldmodel.RelOn, "f1", "a1")} // Finding on Asset
	f := deriveFrontier(nodes, edges)
	if got := onNodesOf(f, MoveExploit); got["a1"] {
		t.Fatalf("已挂 Finding 的 Asset 不应再 exploit，得到 %+v", f)
	}
}

func TestDeriveFrontier_CredentialWithoutAccess_YieldsUseCredential(t *testing.T) {
	nodes := []worldmodel.Node{node("c1", worldmodel.KindCredential)}
	f := deriveFrontier(nodes, nil)
	if got := onNodesOf(f, MoveEscalate); !got["c1"] {
		t.Fatalf("未兑现的 Credential 应产出 use-credential，得到 %+v", f)
	}
}

func TestDeriveFrontier_CredentialEnablesAccess_NoUseCredential(t *testing.T) {
	nodes := []worldmodel.Node{
		node("c1", worldmodel.KindCredential),
		node("ac1", worldmodel.KindAccess),
	}
	edges := []worldmodel.Edge{edge(worldmodel.RelEnables, "c1", "ac1")} // Credential enables Access
	f := deriveFrontier(nodes, edges)
	if got := onNodesOf(f, MoveEscalate); got["c1"] {
		t.Fatalf("已换出 Access 的 Credential 不应再 use-credential，得到 %+v", f)
	}
}

func TestDeriveFrontier_AccessWithoutAsset_YieldsPostExploit(t *testing.T) {
	nodes := []worldmodel.Node{node("ac1", worldmodel.KindAccess)}
	f := deriveFrontier(nodes, nil)
	if got := onNodesOf(f, MovePersist); !got["ac1"] {
		t.Fatalf("新立足点应产出 post-exploit，得到 %+v", f)
	}
}

func TestDeriveFrontier_AccessWithAsset_NoPostExploit(t *testing.T) {
	nodes := []worldmodel.Node{
		node("ac1", worldmodel.KindAccess),
		node("a2", worldmodel.KindAsset),
	}
	edges := []worldmodel.Edge{edge(worldmodel.RelOn, "a2", "ac1")} // Asset on Access（横移已探）
	f := deriveFrontier(nodes, edges)
	if got := onNodesOf(f, MovePersist); got["ac1"] {
		t.Fatalf("已展开横移的立足点不应再 post-exploit，得到 %+v", f)
	}
}

func TestDeriveFrontier_EmptyGraph_EmptyFrontier(t *testing.T) {
	if f := deriveFrontier(nil, nil); len(f) != 0 {
		t.Fatalf("空图应产出空 frontier，得到 %+v", f)
	}
}
