package attackgraph

import (
	"context"
	"strings"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/finding"
)

// Project 投影骨架图（第一趟）：确定性思维链骨架（messages）+ agent 自标 + 成果链（findings）。
//
// 零 LLM、永远能出图——实时阶段用它（Enriched=false，流畅不烧钱）。
// 纯函数：入参为已加载的源记录，无 IO。store 版投影器按 task 拉取后调它（见 store.go）。
// messages 须按 seq 升序传入（事件时序 = 思维链骨干顺序）。
func Project(taskID string, messages []conversation.Message, findings []finding.VulnFinding) Graph {
	b := skeleton(messages)
	return b.assemble(taskID, findings, false)
}

// ProjectEnriched 投影完整图（第一趟骨架 + 第二趟 LLM 语义提炼）。
//
// 在骨架基础上，对 agent 未自标判断（②缺失）的任务线跑 ① LLM 提炼补 hypothesis 节点。
// 扫描结束/手动刷新时用它（Enriched=true）。s 为 nil 时等价于 Project（跳过①）。
func ProjectEnriched(ctx context.Context, taskID string, messages []conversation.Message, findings []finding.VulnFinding, s Summarizer) Graph {
	b := skeleton(messages)
	b.enrich(ctx, s)
	return b.assemble(taskID, findings, s != nil)
}

// assemble 把骨架（可能已 enrich）与成果链拼成最终图：挂接 finding、派生边、标成果路径。
// 骨架趟与提炼趟共用，enriched 标志由调用方按是否跑过①传入。
func (b *builder) assemble(taskID string, findings []finding.VulnFinding, enriched bool) Graph {
	fg := FindingSubgraph(taskID, findings)

	// 成果链挂思维链：finding 挂到证实它的 probe/signal 下（confirms 边由 b.edges() 从 ParentID 派生），
	// 否则 finding 会成为孤儿根、图散成碎片。挂接须在 b.edges() 之前设好 ParentID。
	for i := range fg.Nodes {
		if fg.Nodes[i].Kind != KindFinding {
			continue
		}
		if src, ok := b.findingParent[fg.Nodes[i].ID]; ok {
			fg.Nodes[i].ParentID = src
		}
	}
	for i := range fg.Nodes {
		b.add(fg.Nodes[i]) // 并入成果节点，纳入 id 索引，供 edges() 为其派生 confirms 边
	}

	edges := append(b.edges(), fg.Edges...) // 骨干+confirms（派生）+ depends_on（成果链自带）
	markOnPath(b.nodes)                     // 标记成果路径（通向 finding 的主干），前端据此默认折叠探索段
	collapsed := collapsedSegments(b.nodes) // 折叠段元数据（后端算好，前端只管展开/收起）
	return Graph{TaskID: taskID, Nodes: b.nodes, Edges: edges, Enriched: enriched, Collapsed: collapsed}
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

// collapsedSegments 产出「成果优先」视图的折叠段元数据（后端算好，前端只管展开/收起）。
//
// 成果路径（OnPath）节点默认展开；其余探索/死路节点按「最近 OnPath 祖先」聚成一段——
// 前端在该祖先（Anchor）下画一个占位节点（"N 步已折叠"），点击展开。
// 无 OnPath 祖先的探索节点（不挂在任何主干下）归入开场段（Anchor 空，Opening=true）。
//
// 无任何成果路径（无漏洞）时返回 nil：没有主干可对比，整图照常展开（不把活图折成一坨）。
// 旧版让前端用 nearestKept/hiddenCount 自己重算折叠（复刻后端图知识、且是分歧风险源），此处收归后端。
func collapsedSegments(nodes []Node) []CollapsedSegment {
	idx := make(map[string]int, len(nodes))
	anyOnPath := false
	for i := range nodes {
		idx[nodes[i].ID] = i
		if nodes[i].OnPath {
			anyOnPath = true
		}
	}
	if !anyOnPath {
		return nil // 无主干不折叠
	}

	counts := map[string]int{} // anchor id → 折叠节点数
	var order []string         // anchor 首次出现序（输出稳定，前端节点不抖动）
	for i := range nodes {
		if nodes[i].OnPath {
			continue
		}
		anchor := nearestOnPathAncestor(nodes, idx, nodes[i].ParentID)
		if _, seen := counts[anchor]; !seen {
			order = append(order, anchor)
		}
		counts[anchor]++
	}

	segs := make([]CollapsedSegment, 0, len(order))
	for _, anchor := range order {
		segs = append(segs, CollapsedSegment{
			Anchor:      anchor,
			HiddenCount: counts[anchor],
			Opening:     anchor == "",
		})
	}
	return segs
}

// nearestOnPathAncestor 沿 ParentID 上溯，返回首个 OnPath 节点 id；无（或脏父指针）返回 ""（开场段）。
func nearestOnPathAncestor(nodes []Node, idx map[string]int, parentID string) string {
	for parentID != "" {
		j, ok := idx[parentID]
		if !ok {
			return "" // 脏父指针 → 归开场段
		}
		if nodes[j].OnPath {
			return parentID
		}
		parentID = nodes[j].ParentID
	}
	return ""
}

// findingTitleMax 是漏洞节点短标签的最长字符数（原文不入节点，只留可读摘要，见设计 §6）。
const findingTitleMax = 120

// FindingSubgraph 把一组 finding 投影成成果链子图：
// 每个 finding 一个节点；finding.DependsOn 派生 depends_on 边（前置 → 组合）。
//
// 例：c.DependsOn = [a, b] → 2 条边 {a→c} + {b→c}。
// 跳过空 / 自引用 / 指向不存在 finding 的依赖（防脏数据与孤儿边）。
// 成果链边的唯一来源——原 sitemap.Projector 的 FindingChain 已于本次迁出（见设计 §10）。
func FindingSubgraph(taskID string, findings []finding.VulnFinding) Graph {
	nodes := make([]Node, 0, len(findings))
	ids := make(map[string]bool, len(findings))
	for _, f := range findings {
		ids[f.ID] = true
		nodes = append(nodes, Node{
			ID:         f.ID,
			Kind:       KindFinding,
			Host:       f.Host,
			Title:      firstLine(f.Summary, findingTitleMax),
			Ref:        f.ID,
			Severity:   f.Severity,
			Provenance: ProvDerived,
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

	return Graph{TaskID: taskID, Nodes: nodes, Edges: edges}
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
