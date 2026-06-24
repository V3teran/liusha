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
	tNodes, tEdges := ThinkingChain(messages)
	fg := FindingSubgraph(ownerID, findings)

	nodes := append(append([]Node{}, tNodes...), fg.Nodes...)
	edges := append(append([]Edge{}, tEdges...), fg.Edges...)
	return Graph{OwnerID: ownerID, Nodes: nodes, Edges: edges}
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
