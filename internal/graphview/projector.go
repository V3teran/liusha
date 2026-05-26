// Package graphview 是 sitemap 投影器（仅 active 模式）。
//
// 设计：domain → endpoint → findings（embed 在 endpoint 下）的 2 层结构。
// folder 层（path prefix 分组）不生成——它是显示概念不是攻击面对象，把 endpoint
// 拍平到 domain 直连让辐射图第 1 圈就是真正的攻击面。
//
// 数据源：endpoint 表（commander recon 写）+ finding 表（striker 写）。
// passive 模式无 sitemap 视图（流水账型流量，前端走 findings 列表）。
package graphview

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/activescan"
	"github.com/V3teran/liusha/internal/endpoint"
	"github.com/V3teran/liusha/internal/finding"
)

// 节点 kind 枚举（前端展示用）。
const (
	KindDomain   = "domain"
	KindEndpoint = "endpoint"
)

// FindingSummary 嵌入在 endpoint 节点下的漏洞摘要。
type FindingSummary struct {
	ID       string `json:"id"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	CWEID    string `json:"cwe_id,omitempty"`
}

// FindingChain 是 finding 间的"组合漏洞"依赖边（0059）。
// 从 finding.depends_on uuid[] 数组派生：finding c.DependsOn = [a, b] → 派生 2 条边：
//   {From: a, To: c} + {From: b, To: c}
// 前端 D3 force layout 渲染成虚线弧形，区别于 sitemap 树的实线父子边。
type FindingChain struct {
	From string `json:"from"` // 前置 finding id（被依赖）
	To   string `json:"to"`   // 组合 finding id（依赖 from）
}

// SitemapNode 是 sitemap 节点（domain / endpoint）。
// domain 是 root，children 直接是 endpoint 数组（无中间 folder）。
type SitemapNode struct {
	Kind     string           `json:"kind"`
	Name     string           `json:"name"`               // 显示标签：domain=host / endpoint=name（fallback method+path）
	Path     string           `json:"path,omitempty"`     // 仅 endpoint：URL path
	Method   string           `json:"method,omitempty"`   // 仅 endpoint
	Findings []FindingSummary `json:"findings,omitempty"` // 仅 endpoint，无 findings 时省略
	Children []*SitemapNode   `json:"children,omitempty"` // 仅 domain：直挂 endpoint 数组
}

// View 是 sitemap 投影结果。
type View struct {
	OwnerID     string         `json:"owner_id"`
	Host        string         `json:"host"`
	GeneratedAt time.Time      `json:"generated_at"`
	Root        *SitemapNode   `json:"root"`
	Chains      []FindingChain `json:"chains,omitempty"` // 组合漏洞依赖边（无组合时省略）
}

// FindingReader 是投影器读 finding 表所需的最小接口。
type FindingReader interface {
	ListByOwner(ctx context.Context, ownerType, ownerID string) ([]finding.VulnFinding, error)
}

// EndpointReader 是投影器读 endpoint 表所需的最小接口。
type EndpointReader interface {
	ListByOwner(ctx context.Context, ownerID, host string) ([]endpoint.Endpoint, error)
}

// ActiveReader 是投影器读 active_scan 表所需的最小接口（用于验证 owner 是 active 类型）。
type ActiveReader interface {
	GetByID(ctx context.Context, id string) (activescan.Scan, error)
}

// Projector 是无状态 sitemap 投影器；可全局共享一份。
//
// 仅服务 active 模式——passive 模式无 sitemap 视图（流量是流水账，前端用 findings 列表）。
type Projector struct {
	Findings  FindingReader
	Endpoints EndpointReader
	Active    ActiveReader
}

// Project 投影 (ownerID, host) 范围的 sitemap 树。
//
// owner_id 必须是 active_scan.id；passive_session.id 报错（passive 走 findings 列表）。
// host 为空时显示该 active scan 下全部 host 的合并视图。
func (p *Projector) Project(ctx context.Context, ownerID, host string) (View, error) {
	if ownerID == "" {
		return View{}, fmt.Errorf("owner_id 不能为空")
	}

	// 验证 owner 是 active 模式
	if _, err := p.Active.GetByID(ctx, ownerID); err != nil {
		return View{}, fmt.Errorf("sitemap 仅支持 active 模式 (owner_id=%s 不是 active scan，passive 用 /findings)", ownerID)
	}

	endpoints, err := p.Endpoints.ListByOwner(ctx, ownerID, host)
	if err != nil {
		return View{}, fmt.Errorf("endpoint.ListByOwner: %w", err)
	}

	findings, err := p.Findings.ListByOwner(ctx, "active_scan", ownerID)
	if err != nil {
		return View{}, fmt.Errorf("finding.ListByOwner: %w", err)
	}

	// host 过滤（防 finding.host 跨 host 串）
	if host != "" {
		filtered := findings[:0]
		for _, f := range findings {
			if f.Host == host {
				filtered = append(filtered, f)
			}
		}
		findings = filtered
	}

	// findings 索引分两路：
	//   - 强匹配（method+path 都有）→ findingsByKey: (host, method, path_templated)
	//   - 弱匹配（path 有但 method 缺）→ methodlessByPath: (host, path_templated)，建完 endpoint 树后按 host+path 查唯一 endpoint
	//   - path 缺 → noTargetKey 直接走兜底
	// striker 写 finding 时偶尔漏 method 字段（DOM XSS 实测案例），projector 兜底避免误挂 (no target)
	const noTargetKey = "*|*|*"
	findingsByKey := map[string][]FindingSummary{}
	methodlessByPath := map[string][]FindingSummary{}
	for _, f := range findings {
		method, path := extractFindingTarget(f)
		fs := FindingSummary{
			ID:       f.ID,
			Severity: f.Severity,
			Summary:  firstLine(f.Summary, 120),
			CWEID:    f.CWEID,
		}
		if path == "" {
			findingsByKey[noTargetKey] = append(findingsByKey[noTargetKey], fs)
			continue
		}
		tpath := endpoint.TemplatizePath(path)
		if method == "" {
			methodlessByPath[f.Host+"|"+tpath] = append(methodlessByPath[f.Host+"|"+tpath], fs)
			continue
		}
		key := f.Host + "|" + method + "|" + tpath
		findingsByKey[key] = append(findingsByKey[key], fs)
	}

	// root name：host 参数优先；空时自动派生
	//   - 数据涉及的 host 集合（endpoints + findings union）唯一 → 用它（active scan 多数是单 host）
	//   - 0 或多个 → fallback "(all hosts)"
	rootName := host
	if rootName == "" {
		hostSet := map[string]struct{}{}
		for _, ep := range endpoints {
			if ep.Host != "" {
				hostSet[ep.Host] = struct{}{}
			}
		}
		for _, f := range findings {
			if f.Host != "" {
				hostSet[f.Host] = struct{}{}
			}
		}
		if len(hostSet) == 1 {
			for h := range hostSet {
				rootName = h
			}
		} else {
			rootName = "(all hosts)"
		}
	}
	root := &SitemapNode{Kind: KindDomain, Name: rootName}

	// endpoint 直接挂 domain（无 folder 中间层）+ 建强/弱匹配索引
	// 双侧 TemplatizePath 规范化：历史数据 endpoint.path 可能带尾 "/"（写于 fix 前），
	// finding 侧 path 已 templatize 过——这里也 templatize 避免 key 不匹配
	seenKey := map[string]bool{}
	nodesByPath := map[string][]*SitemapNode{}
	for _, ep := range endpoints {
		tpath := endpoint.TemplatizePath(ep.Path)
		name := ep.Name
		if name == "" {
			name = ep.Method + " " + tpath // fallback display
		}
		node := &SitemapNode{
			Kind:   KindEndpoint,
			Name:   name,
			Path:   tpath,
			Method: ep.Method,
		}
		root.Children = append(root.Children, node)

		key := ep.Host + "|" + ep.Method + "|" + tpath
		seenKey[key] = true
		if fs, ok := findingsByKey[key]; ok {
			node.Findings = append(node.Findings, fs...)
		}
		pathKey := ep.Host + "|" + tpath
		nodesByPath[pathKey] = append(nodesByPath[pathKey], node)
	}

	// 弱匹配：method 缺的 finding 按 host+path 查 endpoint，唯一命中就挂；0 或 >1 都走 (no target)
	for pathKey, fs := range methodlessByPath {
		nodes := nodesByPath[pathKey]
		if len(nodes) == 1 {
			nodes[0].Findings = append(nodes[0].Findings, fs...)
			continue
		}
		findingsByKey[noTargetKey] = append(findingsByKey[noTargetKey], fs...)
	}

	// 剩余 findings 没对应 endpoint（commander 漏 write_endpoint 但 striker 写了 finding）
	// 直接造一个 endpoint 节点挂 domain 下（不再走树形 ensureNode）
	for key, fs := range findingsByKey {
		if seenKey[key] {
			continue
		}
		if key == noTargetKey {
			root.Children = append(root.Children, &SitemapNode{
				Kind: KindEndpoint, Name: "(no target)", Findings: fs,
			})
			continue
		}
		parts := strings.SplitN(key, "|", 3)
		method, path := parts[1], parts[2]
		root.Children = append(root.Children, &SitemapNode{
			Kind:     KindEndpoint,
			Name:     method + " " + path,
			Path:     path,
			Method:   method,
			Findings: fs,
		})
	}

	// 按 path 字典序稳定排
	sort.SliceStable(root.Children, func(i, j int) bool {
		return root.Children[i].Path < root.Children[j].Path
	})

	// 构建 chains：遍历 findings.DependsOn 派生组合漏洞依赖边
	// finding c.DependsOn = [a, b] → chains 加 2 条：{a→c, b→c}
	// 跳过自引用（防 bad data）和指向不存在 finding 的边（防孤儿）
	findingIDs := make(map[string]bool, len(findings))
	for _, f := range findings {
		findingIDs[f.ID] = true
	}
	var chains []FindingChain
	for _, f := range findings {
		for _, dep := range f.DependsOn {
			if dep == "" || dep == f.ID || !findingIDs[dep] {
				continue
			}
			chains = append(chains, FindingChain{From: dep, To: f.ID})
		}
	}

	return View{
		OwnerID:     ownerID,
		Host:        host,
		GeneratedAt: time.Now().UTC(),
		Root:        root,
		Chains:      chains,
	}, nil
}

// extractFindingTarget 从 finding.target jsonb 提取 method + path。
// commander/striker 写入时已带 method+path 字段，简化解析（不再 fallback url）。
func extractFindingTarget(f finding.VulnFinding) (method, path string) {
	if len(f.Target) == 0 {
		return "", ""
	}
	var m map[string]any
	if err := json.Unmarshal(f.Target, &m); err != nil {
		return "", ""
	}
	if v, ok := m["method"].(string); ok {
		method = strings.ToUpper(v)
	}
	if v, ok := m["path"].(string); ok {
		path = v
	}
	return method, path
}

func firstLine(s string, max int) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if max > 0 && len(s) > max {
		s = s[:max]
	}
	return s
}
