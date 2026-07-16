// Package sitemap 是攻击面 sitemap 投影器（仅 active 模式）。
//
// 设计：domain → endpoint → findings（embed 在 endpoint 下）的 2 层结构。
// folder 层（path prefix 分组）不生成——它是显示概念不是攻击面对象，把 endpoint
// 拍平到 domain 直连让辐射图第 1 圈就是真正的攻击面。
//
// 数据源（单一真相源）：agent_traffic（DistinctRoutes 去重派生攻击面路由，按 task）
// + finding 表（exploitation 写）。攻击面不再靠手动 endpoint 表/write_endpoint 转写——
// recon 工具流量经 sandbox 自动入 agent_traffic，sitemap 从中派生（参数自动入库）。
// passive 模式无 sitemap 视图（流水账型流量，前端走 findings 列表）。
package sitemap

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/flow"
	"github.com/V3teran/liusha/internal/task"
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

// SitemapNode 是 sitemap 节点（domain / endpoint）。
// domain 是 root，children 直接是 endpoint 数组（无中间 folder）。
type SitemapNode struct {
	Kind     string           `json:"kind"`
	Name     string           `json:"name"`               // 显示标签：domain=host / endpoint=代表响应 <title>（抽不到 fallback method+path）
	Path     string           `json:"path,omitempty"`     // 仅 endpoint：URL path（已 templatize）
	Method   string           `json:"method,omitempty"`   // 仅 endpoint
	Findings []FindingSummary `json:"findings,omitempty"` // 仅 endpoint，无 findings 时省略
	Children []*SitemapNode   `json:"children,omitempty"` // 仅 domain：直挂 endpoint 数组
}

// View 是 sitemap 投影结果（纯端点树）。
// 成果链（finding 组合依赖边）已迁出至 internal/attackgraph 执行图投影器，
// 见 docs/attack-graph-design.md §10。
type View struct {
	TaskID      string       `json:"task_id"`
	Host        string       `json:"host"`
	GeneratedAt time.Time    `json:"generated_at"`
	Root        *SitemapNode `json:"root"`
}

// FindingReader 是投影器读 finding 表所需的最小接口。
type FindingReader interface {
	ListByTask(ctx context.Context, taskID string) ([]finding.VulnFinding, error)
}

// FlowReader 是投影器读 agent_traffic 派生攻击面路由所需的最小接口。
// *flow.AgentStore 自动满足。带代表响应体片段供抽 <title> 作 UI 名。
type FlowReader interface {
	DistinctRoutesWithRepresentative(ctx context.Context, taskID, host string) ([]flow.RouteRepr, error)
}

// TaskReader 是投影器读 task 表所需的最小接口（验证 task 是 active 模式）。
type TaskReader interface {
	GetByID(ctx context.Context, id string) (task.Task, error)
}

// Projector 是无状态 sitemap 投影器；可全局共享一份。
//
// 仅服务 active 模式——passive 模式无 sitemap 视图（流量是流水账，前端用 findings 列表）。
type Projector struct {
	Findings FindingReader
	Flows    FlowReader
	Tasks    TaskReader
}

// Project 投影 (taskID, host) 范围的 sitemap 树。
//
// task 必须是 active 模式；passive task 报错（passive 走 findings 列表）。
// host 为空时显示该 task 下全部 host 的合并视图。
func (p *Projector) Project(ctx context.Context, taskID, host string) (View, error) {
	if taskID == "" {
		return View{}, fmt.Errorf("task_id 不能为空")
	}

	// 验证 task 是 active 模式
	t, err := p.Tasks.GetByID(ctx, taskID)
	if err != nil {
		return View{}, fmt.Errorf("sitemap: 读 task %s 失败: %w", taskID, err)
	}
	if t.Mode != task.ModeActive {
		return View{}, fmt.Errorf("sitemap 仅支持 active 模式 (task=%s 是 %s，passive 用 /findings)", taskID, t.Mode)
	}

	routes, err := p.Flows.DistinctRoutesWithRepresentative(ctx, taskID, host)
	if err != nil {
		return View{}, fmt.Errorf("flow.DistinctRoutesWithRepresentative: %w", err)
	}

	findings, err := p.Findings.ListByTask(ctx, taskID)
	if err != nil {
		return View{}, fmt.Errorf("finding.ListByTask: %w", err)
	}

	// host 过滤（防 finding.host 跨 host 串）。
	// finding.host 带端口（111.229.193.40:34280）、agent_traffic.host 是裸 host——两侧都剥端口再比，
	// 否则跨 host 过滤恒空、或下方 finding 永远匹配不上路由节点。
	if host != "" {
		want := stripHostPort(host)
		filtered := findings[:0]
		for _, f := range findings {
			if stripHostPort(f.Host) == want {
				filtered = append(filtered, f)
			}
		}
		findings = filtered
	}

	// findings 索引分两路：
	//   - 强匹配（method+path 都有）→ findingsByKey: (host, method, path_templated)
	//   - 弱匹配（path 有但 method 缺）→ methodlessByPath: (host, path_templated)，建完 endpoint 树后按 host+path 查唯一 endpoint
	//   - path 缺 → noTargetKey 直接走兜底
	// exploitation 写 finding 时偶尔漏 method 字段（DOM XSS 实测案例），projector 兜底避免误挂 (no target)
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
		// finding.host 带端口，路由节点 key 用裸 host——剥端口对齐，否则 finding 匹配不上路由、
		// 落进下方"孤儿 finding"兜底造出重复裸节点（同一 path 出现两次）。
		fhost := stripHostPort(f.Host)
		tpath := TemplatizePath(path)
		if method == "" {
			methodlessByPath[fhost+"|"+tpath] = append(methodlessByPath[fhost+"|"+tpath], fs)
			continue
		}
		key := fhost + "|" + method + "|" + tpath
		findingsByKey[key] = append(findingsByKey[key], fs)
	}

	// root name：host 参数优先；空时自动派生
	//   - 数据涉及的 host 集合（routes + findings union）唯一 → 用它（active scan 多数是单 host）
	//   - 0 或多个 → fallback "(all hosts)"
	rootName := host
	if rootName == "" {
		// 用裸 host 判"是否单一 host"（两侧剥端口：routes.host 已裸、finding.host 带端口——
		// 不剥会把同一真实 host 算成 2 个，误落 "(all hosts)"）；
		// 但显示用真实 host:port（hostPortByBare 从 routes 的 url authority 拿，保留端口）。
		hostSet := map[string]struct{}{}
		hostPortByBare := map[string]string{}
		for _, r := range routes {
			if r.Host != "" {
				hostSet[r.Host] = struct{}{}
				if r.HostPort != "" {
					hostPortByBare[r.Host] = r.HostPort // 同一裸 host 的 host:port 取首个即可
				}
			}
		}
		for _, f := range findings {
			if f.Host != "" {
				hostSet[stripHostPort(f.Host)] = struct{}{}
			}
		}
		if len(hostSet) == 1 {
			for h := range hostSet {
				if hp := hostPortByBare[h]; hp != "" {
					rootName = hp // 显示真实地址含端口
				} else {
					rootName = h
				}
			}
		} else {
			rootName = "(all hosts)"
		}
	}
	root := &SitemapNode{Kind: KindDomain, Name: rootName}

	// 派生路由直接挂 domain（无 folder 中间层）+ 建强/弱匹配索引。
	// DistinctRoutesWithRepresentative 已 SQL 去重 (host, method, raw_path)，这里 TemplatizePath 再折叠 ID 变体
	//   （/user/1 与 /user/2 → /user/:id），故按 templatize 后的 key 二次去重避免重复节点。
	// 双侧 TemplatizePath 规范化让 finding 侧（也 templatize 过）能精确命中路由。
	seenKey := map[string]bool{}
	nodesByPath := map[string][]*SitemapNode{}
	for _, r := range routes {
		method := strings.ToUpper(r.Method)
		tpath := TemplatizePath(r.Path)
		key := r.Host + "|" + method + "|" + tpath
		if seenKey[key] {
			continue // templatize 后重复（如 /user/1 与 /user/2）已挂过节点
		}
		seenKey[key] = true

		// UI 名：优先代表响应的 <title>（人类可读功能名），抽不到 fallback method+path。
		name := extractTitle(r.BodyHead)
		if name == "" {
			name = method + " " + tpath
		}
		node := &SitemapNode{
			Kind:   KindEndpoint,
			Name:   name,
			Path:   tpath,
			Method: method,
		}
		root.Children = append(root.Children, node)

		if fs, ok := findingsByKey[key]; ok {
			node.Findings = append(node.Findings, fs...)
		}
		pathKey := r.Host + "|" + tpath
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

	// 剩余 findings 没对应派生路由（exploitation 写了 finding 但该路由未被流量捕获）
	// 直接造一个 endpoint 节点挂 domain 下
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

	return View{
		TaskID:      taskID,
		Host:        host,
		GeneratedAt: time.Now().UTC(),
		Root:        root,
	}, nil
}

// extractFindingTarget 从 finding.target jsonb 提取 method + path。
// orchestrator/exploitation 写入时已带 method+path 字段，简化解析（不再 fallback url）。
func extractFindingTarget(f finding.VulnFinding) (method, path string) {
	if len(f.Target) == 0 {
		return "", ""
	}
	var m map[string]any
	if err := json.Unmarshal(f.Target, &m); err != nil {
		return "", ""
	}
	if v, ok := m["method"].(string); ok {
		// LLM 偶尔写多 method 值（"GET+POST" / "GET/POST"），取第一个真实 method——
		// 否则匹配不上单 method 的路由节点、又造出冗余裸节点。按 +/、空格 等分隔符切首段。
		parts := strings.FieldsFunc(v, func(r rune) bool {
			return r == '+' || r == '/' || r == ',' || r == ' ' || r == '|'
		})
		if len(parts) > 0 {
			method = strings.ToUpper(parts[0])
		}
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

// stripHostPort 把 host:port 归一化为裸 host，对齐 agent_traffic.host 存储键（去端口）。
// finding.host 带端口、routes.host 裸——两侧统一后才能匹配。无端口时原样返回。
func stripHostPort(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

// titleRe 抓 HTML <title>…</title>（i=忽略大小写，s=. 匹配换行，非贪婪取第一段）。
var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

const maxTitleLen = 80

// extractTitle 从代表响应体片段抽 <title> 作 sitemap 节点 UI 名；抽不到返空（caller fallback method+path）。
//
//   - gzip：响应体可能 gzip 压缩（mitmproxy 存 raw_content）。识别 gzip 魔数后尽力解压——
//     截断的 gzip 流解到 <head> 处即够（<title> 在最前），ErrUnexpectedEOF 忽略。
//   - 实体/空白：HTML 实体解码（&amp;→&）+ 折叠连续空白；超长截断。
//   - SPA / 无 title 的 API/XHR 响应：返空，优雅降级到 method+path。
func extractTitle(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	body := raw
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b { // gzip 魔数
		if zr, err := gzip.NewReader(bytes.NewReader(raw)); err == nil {
			if dec, _ := io.ReadAll(io.LimitReader(zr, 64*1024)); len(dec) > 0 {
				body = dec // 截断 gzip 的 ReadAll 报错但已读出 <head>，取 dec 即可
			}
		}
	}
	m := titleRe.FindSubmatch(body)
	if m == nil {
		return ""
	}
	title := strings.Join(strings.Fields(html.UnescapeString(string(m[1]))), " ")
	if r := []rune(title); len(r) > maxTitleLen { // 按 rune 截断，避免切半 CJK 多字节字符
		title = string(r[:maxTitleLen])
	}
	return title
}
