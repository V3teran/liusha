package planner

import (
	"encoding/json"

	"github.com/V3teran/liusha/internal/worldmodel"
)

// deriveFrontier 是纯函数硬核：给定世界模型快照，推导未展开的攻击面。
// 「已展开」由出边覆盖判定，未被覆盖的源节点即 frontier。
func deriveFrontier(nodes []worldmodel.Node, edges []worldmodel.Edge) []Move {
	idx := indexNodes(nodes)
	cov := computeCoverage(edges, idx)

	var frontier []Move
	for _, n := range nodes {
		switch n.Kind {
		case worldmodel.KindTarget:
			// Target 节点无 Asset 出边 → enumerate（全场景信息收集入口）
			if !cov.hasAssetOn[n.ID] {
				frontier = append(frontier, Move{
					Kind: MoveEnumerate, Target: n.Ref, OnNodeID: n.ID,
					Reason: "目标尚未枚举出资产",
				})
			}
		case worldmodel.KindAsset:
			// Asset 节点无 Finding 出边 → probe（弱点探测，探测后产出 Finding）
			if !cov.hasFindingOn[n.ID] {
				frontier = append(frontier, Move{
					Kind: MoveProbe, Target: n.Ref, OnNodeID: n.ID,
					Reason: "资产尚未探测弱点",
				})
			}
		case worldmodel.KindFinding:
			// Finding 节点（高置信弱点）无 Access 出边 → exploit（坐实漏洞获得立足点）
			if n.Confidence == worldmodel.ConfConfirmed && !cov.enablesAccess[n.ID] {
				frontier = append(frontier, Move{
					Kind: MoveExploit, Target: n.Ref, OnNodeID: n.ID,
					Reason: "已确证弱点尚未坐实利用",
				})
			}
		case worldmodel.KindAccess:
			// Access 节点无横向 Asset 出边 → escalate（从立足点扩展攻击面）
			if !cov.hasLateralAsset[n.ID] {
				frontier = append(frontier, Move{
					Kind: MoveEscalate, Target: n.Ref, OnNodeID: n.ID,
					Reason: "立足点尚未横向移动/提权",
				})
			}
			// Access 节点有 data/flag 价值标记 → persist（数据价值变现）
			if hasValueFlag(n) && !cov.hasPersisted[n.ID] {
				frontier = append(frontier, Move{
					Kind: MovePersist, Target: n.Ref, OnNodeID: n.ID,
					Reason: "立足点有数据价值待采集/持久化",
				})
			}
		}
	}
	return frontier
}

// hasValueFlag 检查节点是否标记了数据价值（flag、敏感数据等）。
// 从 Node.Attrs 读取 JSON 字段。
func hasValueFlag(n worldmodel.Node) bool {
	if len(n.Attrs) == 0 {
		return false
	}
	var attrs map[string]interface{}
	if err := json.Unmarshal(n.Attrs, &attrs); err != nil {
		return false
	}
	if v, ok := attrs["value"].(string); ok && v != "" {
		return true
	}
	if tags, ok := attrs["tags"].([]interface{}); ok {
		for _, t := range tags {
			if s, ok := t.(string); ok {
				if s == "flag" || s == "sensitive_data" || s == "high_value" {
					return true
				}
			}
		}
	}
	return false
}

type coverage struct {
	hasAssetOn      map[string]bool // 有 Asset on 它
	hasFindingOn    map[string]bool // 有 Finding on 它
	enablesAccess   map[string]bool // 它 enables 某 Access
	hasLateralAsset map[string]bool // Access 节点有横向 Asset 出边
	hasPersisted    map[string]bool // Access 节点已执行 persist Move
}

func indexNodes(nodes []worldmodel.Node) map[string]worldmodel.Node {
	idx := make(map[string]worldmodel.Node, len(nodes))
	for _, n := range nodes {
		idx[n.ID] = n
	}
	return idx
}

// computeCoverage 单遍扫边归类覆盖。on 边 Src=附着者、Dst=被附着者；
// enables 边 Src=能力源(Credential/Finding)、Dst=获得物(Access)；
// derives 边追踪横向移动（Access → Asset）和持久化（Access → 价值节点）。
func computeCoverage(edges []worldmodel.Edge, idx map[string]worldmodel.Node) coverage {
	cov := coverage{
		hasAssetOn:      map[string]bool{},
		hasFindingOn:    map[string]bool{},
		enablesAccess:   map[string]bool{},
		hasLateralAsset: map[string]bool{},
		hasPersisted:    map[string]bool{},
	}
	for _, e := range edges {
		src, dst := idx[e.Src], idx[e.Dst]
		switch e.Rel {
		case worldmodel.RelOn:
			switch src.Kind {
			case worldmodel.KindAsset:
				cov.hasAssetOn[e.Dst] = true
			case worldmodel.KindFinding:
				cov.hasFindingOn[e.Dst] = true
			}
		case worldmodel.RelEnables:
			if dst.Kind == worldmodel.KindAccess {
				cov.enablesAccess[e.Src] = true
			}
		case worldmodel.RelDerives:
			// Access → Asset（横向移动）
			if src.Kind == worldmodel.KindAccess && dst.Kind == worldmodel.KindAsset {
				cov.hasLateralAsset[e.Src] = true
			}
			// Access → 价值节点（持久化完成）
			if src.Kind == worldmodel.KindAccess && hasValueFlag(dst) {
				cov.hasPersisted[e.Src] = true
			}
		}
	}
	return cov
}
