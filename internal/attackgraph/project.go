package attackgraph

import (
	"strings"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/finding"
)

// Project 把一次扫描的对话事件流 + 漏洞投影成完整图：思维链（messages）+ 成果链（findings）。
//
// 纯函数：入参为已加载的源记录，无 IO。store 版投影器按 owner 拉取后调它（见 store.go）。
// messages 须按 seq 升序传入（事件时序 = 思维链骨干顺序）。
func Project(ownerID string, messages []conversation.Message, findings []finding.VulnFinding) Graph {
	tNodes, tEdges, findingParent := ThinkingChain(messages)
	fg := FindingSubgraph(ownerID, findings)

	// 成果链挂思维链：finding 挂到产出它的 write_finding 动作下 + evidence 边（动作→finding），
	// 否则 finding 会成为孤儿根、图散成碎片。
	var evidenceEdges []Edge
	for i := range fg.Nodes {
		if fg.Nodes[i].Kind != KindFinding {
			continue
		}
		if act, ok := findingParent[fg.Nodes[i].ID]; ok {
			fg.Nodes[i].ParentID = act
			evidenceEdges = append(evidenceEdges, Edge{From: act, To: fg.Nodes[i].ID, Type: EdgeEvidence})
		}
	}

	nodes := append(append([]Node{}, tNodes...), fg.Nodes...)
	edges := append(append(append([]Edge{}, tEdges...), fg.Edges...), evidenceEdges...)
	markOnPath(nodes) // 标记成果路径（通向 finding 的主干），前端「成果优先」据此默认折叠死路
	return Graph{OwnerID: ownerID, Nodes: nodes, Edges: edges}
}

// markOnPath 标记「成果路径」节点：从每个 finding 沿 ParentID 上溯到根，
// 路径上的 想/做/派/漏洞 全置 OnPath=true；其余是死路/探索（OnPath=false）。
//
// 这是「成果优先」视图的数据基础——把上千步轨迹里真正通向漏洞的主干择出来。
// 死路不删（轨迹留全，调试/审计仍可下钻），只供前端在视图层默认折叠。
// 复杂度 O(路径总长)：每个节点最多被标记一次（已标记即剪枝上溯）。
func markOnPath(nodes []Node) {
	idx := make(map[string]int, len(nodes))
	for i := range nodes {
		idx[nodes[i].ID] = i
	}
	for i := range nodes {
		if nodes[i].Kind != KindFinding {
			continue
		}
		for cur := nodes[i].ID; cur != ""; {
			j, ok := idx[cur]
			if !ok || nodes[j].OnPath {
				break // 越界（脏父指针）/ 已标记 → 剪枝
			}
			nodes[j].OnPath = true
			cur = nodes[j].ParentID
		}
	}
}

// findingTitleMax 是漏洞节点短标签的最长字符数（原文不入节点，只留可读摘要，见设计 §6）。
const findingTitleMax = 120

// FindingSubgraph 把一组 finding 投影成成果链子图：
// 每个 finding 一个节点；finding.DependsOn 派生 depends_on 边（前置 → 组合）。
//
// 例：c.DependsOn = [a, b] → 2 条边 {a→c} + {b→c}。
// 跳过空 / 自引用 / 指向不存在 finding 的依赖（防脏数据与孤儿边）。
// 成果链边的唯一来源——原 sitemap.Projector 的 FindingChain 已于本次迁出（见设计 §10）。
func FindingSubgraph(ownerID string, findings []finding.VulnFinding) Graph {
	nodes := make([]Node, 0, len(findings))
	ids := make(map[string]bool, len(findings))
	for _, f := range findings {
		ids[f.ID] = true
		nodes = append(nodes, Node{
			ID:       f.ID,
			Kind:     KindFinding,
			Target:   f.Host,
			Title:    firstLine(f.Summary, findingTitleMax),
			Ref:      f.ID,
			Severity: f.Severity,
		})
	}

	var edges []Edge
	for _, f := range findings {
		for _, dep := range f.DependsOn {
			if dep == "" || dep == f.ID || !ids[dep] {
				continue
			}
			edges = append(edges, Edge{From: dep, To: f.ID, Type: EdgeDependsOn})
		}
	}

	return Graph{OwnerID: ownerID, Nodes: nodes, Edges: edges}
}

// firstLine 取首行并按 rune 截断到 max（避免切断多字节 CJK 字符）。max<=0 不截断。
func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if r := []rune(s); max > 0 && len(r) > max {
		return string(r[:max])
	}
	return s
}
