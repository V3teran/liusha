package hunter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools/manifest"
)

func buildUserPrompt(ctx context.Context, deps Deps, p skill.BuilderParams) string {
	bodyLimit := deps.UserPromptBodyLimit
	if bodyLimit <= 0 {
		bodyLimit = 8192
	}

	var b strings.Builder

	// 字段触发渲染（不再 Mode-driven）：
	//   - RequestHeaders 非空或 URL 非空 → 渲染 raw HTTP 段（trafficAnalysis / 带 flow_id 的 exploitation）
	//   - Brief 非空 → 渲染 brief 段（orchestrator / exploitation）
	// 两者可并存：trafficAnalysis spawn exploitation 带 flow_id 时，exploitation 同时看到 raw HTTP + brief。
	if len(p.RequestHeaders) > 0 || p.URL != "" {
		// 段 0（0060+ 流量字典）：本流量已入 http_flow 表，告诉 LLM flow_id 让它能用
		// replay_flow(id=N, modifications={...}) 改参数重发——比手写 curl 准 100 倍，
		// 自动继承 cookie/CSRF/auth header/其它 form 字段。
		if p.FlowID > 0 {
			fmt.Fprintf(&b, "## 当前流量\n\n本流量 `flow_id=%d`。复用此请求改某参数 fuzz / IDOR / 注 payload，"+
				"调 `replay_flow(id=%d, modifications={...})`，工具自动继承所有 header / cookie / form 字段。\n\n",
				p.FlowID, p.FlowID)
		}

		// 段 1: 请求
		// raw HTTP/1.1 协议形式打印——含 Host 头，LLM 不需要猜 target，
		// 直接拼 `http://{Host}{URL}` 喂给 sqlmap/curl 等工具即可。
		b.WriteString("## 流量请求\n\n```\n")
		b.WriteString(strings.ToUpper(p.Method))
		b.WriteString(" ")
		b.WriteString(p.URL)
		b.WriteString(" HTTP/1.1\nHost: ")
		b.WriteString(p.Host)
		b.WriteString("\n```\n\n### Request Headers\n\n")
		writeHeadersBlock(&b, p.RequestHeaders)
		b.WriteString("\n### Request Body")
		writeBodyBlock(&b, p.RequestBody, bodyLimit)

		// 段 2: 响应
		b.WriteString("\n\n## 流量响应\n\n```\nHTTP/1.1 ")
		if p.ResponseStatus > 0 {
			fmt.Fprintf(&b, "%d", p.ResponseStatus)
		} else {
			b.WriteString("(unknown)")
		}
		b.WriteString("\n```\n\n### Response Headers\n\n")
		writeHeadersBlock(&b, p.ResponseHeaders)
		b.WriteString("\n### Response Body")
		writeBodyBlock(&b, p.ResponseBody, bodyLimit)
	}
	if p.Brief != "" {
		// 段 3 (orchestrator 独有) 或 段 1 (exploitation 无 flow): 自然语言 brief。
		// exploitation brief 是 orchestrator LLM 写的指令；orchestrator 同时传 flow_id 时，本段位于流量段之后。
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## 站点任务\n\n%s\n", p.Brief)
		// active 路径强制注入 host（orchestrator/exploitation 都看得见）。
		// 否则 orchestrator prompt 写"不要在 brief 里复述站点 URL（host 自动注入）"，
		// 但 buildUserPrompt 在 brief-only 路径下原本不渲染 host —— exploitation 只能猜
		// localhost / dvwa 等，公网 host 全 502/404，挖不到 finding。
		if p.Host != "" {
			fmt.Fprintf(&b, "\n## 目标 Host\n\n`%s`\n", p.Host)
		}
	}

	findingsLimit := deps.FindingsLimit
	if findingsLimit <= 0 {
		findingsLimit = 100
	}
	lessonsLimit := deps.LessonsLimit
	if lessonsLimit <= 0 {
		lessonsLimit = 100
	}

	// 段 3: 该 host 已有 finding（限本次 owner，不跨次扫描）
	if existing := loadExistingFindings(ctx, deps.Findings, p.OwnerType, p.OwnerID, p.Host, findingsLimit); existing != "" {
		b.WriteString("\n\n## 该 host 已有 finding（本次扫描内）\n\n")
		b.WriteString(existing)
	}

	// notes 笔记板段已退役——agent 思路改输出到对话（reasoning），跨 task 上下文走对话历史。

	// 段 4: lesson + hint（跨 owner 长期经验）
	if knowledge := loadKnowledgeForPrompt(ctx, deps.Lessons, p.Host, lessonsLimit); knowledge != "" {
		b.WriteString("\n\n")
		b.WriteString(knowledge)
	}

	// 段 4.5: Tier 1 工具索引（Progressive Disclosure）——
	// 列出沙箱内所有可调外部 CLI 工具的 name + 一句话用途，来源 tools.yaml（与 Dockerfile 同步）。
	// 详情按需调 read_tooling_skill(name) 拉 SKILL.md，不在 prompt 常驻。
	if catalog := buildToolingCatalog(deps.ToolsManifest); catalog != "" {
		b.WriteString("\n\n")
		b.WriteString(catalog)
	}

	// 段 4.6: Tier 1 漏洞挖掘指南索引——按 category 分组（与 tooling 同模式）。
	// 详情按需调 read_vuln_skill(name) 拉，不在 prompt 常驻。
	if catalog := buildVulnCatalog(deps.VulnLoader); catalog != "" {
		b.WriteString("\n\n")
		b.WriteString(catalog)
	}

	return b.String()
}

// categoryItem 是索引段的分类条目（key + 渲染 label）。
// tooling 与 vuln 各维护一份独立的 categoryOrder，renderCatalog 统一渲染。
type categoryItem struct {
	Key   string
	Label string
}

// catalogEntry 是索引段渲染用的扁平条目（name + 一句话用途），
// 屏蔽 manifest.Tool 与 skill.Card 的类型差异，让 renderCatalog 复用同一套渲染。
type catalogEntry struct {
	Name string
	Desc string
}

// toolingCategoryOrder 是工具索引段的固定渲染顺序——
// 与 PTES/OWASP 渗透阶段流水线对齐：侦察 → 发现 → 漏扫 → 利用 → 辅助。
// 顺序固定让 prompt cache 命中率最高（同一批工具集 → 同一 prefix）。
// 未在本表内的 category（含空值）→ 落入末尾的"未分类"组，提醒维护者补 frontmatter。
var toolingCategoryOrder = []categoryItem{
	{"recon", "recon（侦察 — 资产/服务/技术栈发现）"},
	{"discovery", "discovery（内容/参数发现）"},
	{"vulnscan", "vulnscan（自动化模板漏扫）"},
	{"injection", "injection（注入类专项）"},
	{"deserialization", "deserialization（反序列化 payload 生成）"},
	{"auth", "auth（认证/凭证攻击）"},
	{"sast", "sast（源码静态分析）"},
	{"utility", "utility（通用辅助：HTTP/JSON/脚本/OOB）"},
}

// vulnCategoryOrder 是漏洞挖掘指南索引段的固定渲染顺序——
// 按"领域 + 形态"切分；未来扩展到 cloud/container/post-exploit 时在此追加 key。
// 当前阶段（web 主导）只有一类，但分组结构与 tooling 保持一致，框架先立起来。
var vulnCategoryOrder = []categoryItem{
	{"web", "web（Web 应用漏洞）"},
}

// buildToolingCatalog 渲染工具索引段——数据来自 ToolsManifest（tools.yaml，与 Dockerfile 同步）。
// 与 vuln 不同：工具是否存在由 manifest 决定，SKILL.md 仅是可选详细手册（按需 read_tooling_skill 拉）。
func buildToolingCatalog(m *manifest.Manifest) string {
	if m == nil || len(m.Tools) == 0 {
		return ""
	}
	buckets := make(map[string][]catalogEntry, 8)
	for cat, tools := range m.ByCategory() {
		for _, t := range tools {
			buckets[cat] = append(buckets[cat], catalogEntry{t.Name, t.Description})
		}
	}
	return renderCatalog("## 可用外部工具索引\n", toolingCategoryOrder, buckets,
		"建议补 tools.yaml category 或 toolingCategoryOrder")
}

// renderCatalog 是工具/漏洞索引段的公共渲染逻辑：已知分类按 order 固定顺序输出
// （稳定 prompt 顺序、cache 友好），未匹配的落入"未分类"组并以 unclassifiedHint
// 提示维护者补哪个字段。每组内按 name 字典序；buckets 为空返回空串。
// header 由调用方提供（含末尾换行）。
func renderCatalog(header string, order []categoryItem, buckets map[string][]catalogEntry, unclassifiedHint string) string {
	if len(buckets) == 0 {
		return ""
	}
	for k := range buckets {
		sortEntriesByName(buckets[k])
	}

	var b strings.Builder
	b.WriteString(header)

	for _, cat := range order {
		group := buckets[cat.Key]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n", cat.Label)
		for _, e := range group {
			fmt.Fprintf(&b, "- **%s**: %s\n", e.Name, e.Desc)
		}
		delete(buckets, cat.Key)
	}

	if len(buckets) > 0 {
		fmt.Fprintf(&b, "\n### 未分类（%s）\n\n", unclassifiedHint)
		rest := make([]string, 0, len(buckets))
		for k := range buckets {
			rest = append(rest, k)
		}
		sortStrings(rest)
		for _, k := range rest {
			for _, e := range buckets[k] {
				if k == "" {
					fmt.Fprintf(&b, "- **%s**: %s\n", e.Name, e.Desc)
				} else {
					fmt.Fprintf(&b, "- **%s** (category=%s): %s\n", e.Name, k, e.Desc)
				}
			}
		}
	}

	return b.String()
}

// buildSkillCatalog 把 skill.Loader（每个 SKILL.md 一张 Card）转成索引段。
func buildSkillCatalog(loader *skill.Loader, header string, order []categoryItem) string {
	if loader == nil {
		return ""
	}
	cards := loader.List()
	if len(cards) == 0 {
		return ""
	}
	buckets := make(map[string][]catalogEntry, len(order)+1)
	for _, c := range cards {
		buckets[c.Category] = append(buckets[c.Category], catalogEntry{c.Name, c.Description})
	}
	return renderCatalog(header, order, buckets, "建议补 frontmatter category 字段")
}

// buildVulnCatalog 渲染漏洞挖掘指南索引段（薄 wrapper —— 委托 buildSkillCatalog）。
// 当前阶段 web 主导只有一类，但分组结构与 tooling 一致，未来扩展按 vulnCategoryOrder 追加。
func buildVulnCatalog(loader *skill.Loader) string {
	header := "## 可用漏洞挖掘指南索引\n"
	body := buildSkillCatalog(loader, header, vulnCategoryOrder)
	if body == "" {
		return ""
	}
	// 明确告诉 LLM 不要凭"行业常识"猜不在列表的 name——SKILL 库可能不完整。
	body += "\n> 列表外的漏洞类型**不要调** `read_vuln_skill`（会直接报错），凭工具知识 + sqlmap/dalfox/nuclei 等直接挖即可。\n"
	return body
}

// sortEntriesByName 按 Name 对索引条目做插入排序——每分类 1-5 条，开销可忽略，省一个 sort 包 import。
func sortEntriesByName(es []catalogEntry) {
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && es[j-1].Name > es[j].Name; j-- {
			es[j-1], es[j] = es[j], es[j-1]
		}
	}
}

// sortStrings 按字典序对字符串切片排序——同样小切片插排。
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && ss[j-1] > ss[j]; j-- {
			ss[j-1], ss[j] = ss[j], ss[j-1]
		}
	}
}

func writeHeadersBlock(b *strings.Builder, headers json.RawMessage) {
	if len(headers) == 0 {
		b.WriteString("（无 headers）\n")
		return
	}
	if pretty, err := json.MarshalIndent(headers, "", "  "); err == nil && len(pretty) > 0 {
		b.WriteString("```json\n")
		b.Write(pretty)
		b.WriteString("\n```\n")
		return
	}
	b.WriteString("```\n")
	b.Write(headers)
	b.WriteString("\n```\n")
}

func writeBodyBlock(b *strings.Builder, body []byte, limit int) {
	switch {
	case len(body) == 0:
		b.WriteString("\n\n（空）\n")
	case len(body) > limit:
		fmt.Fprintf(b, "（截断到前 %d 字节，原总长 %d）\n\n", limit, len(body))
		b.WriteString("```\n")
		b.Write(body[:limit])
		b.WriteString("\n```\n")
	default:
		b.WriteString("\n\n```\n")
		b.Write(body)
		b.WriteString("\n```\n")
	}
}

// loadExistingFindings 拉「owner + host」已有 finding 摘要（dedup 参考）。
//
// 范围限 owner+host，每次扫描独立，不被跨次扫描的历史污染（复用走 lesson，
// 由 loadKnowledgeForPrompt 注入段 4）。
// showLimit 由 caller 提供；SQL 拉 showLimit+1 条做"还有更多"信号。
//
// 双轨切读：按 (ownerType, ownerID) 查 finding（passive_session / active_scan 维度）。
// 任一为空（旧 enqueue 路径未填）则跳过——可接受过渡期损失，回显需 B5+ 数据回填。
func loadExistingFindings(ctx context.Context, store *finding.Store, ownerType, ownerID, host string, showLimit int) string {
	if store == nil || ownerType == "" || ownerID == "" || host == "" || showLimit <= 0 {
		return ""
	}
	fs, err := store.ListByOwnerAndHost(ctx, ownerType, ownerID, host, showLimit+1)
	if err != nil || len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range fs {
		if i >= showLimit {
			b.WriteString("...（可能还有更多；用 read_findings 查全）\n")
			break
		}
		fmt.Fprintf(&b, "- [%s] %s\n", f.Severity, firstLine(f.Summary, 120))
	}
	return b.String()
}

// loadKnowledgeForPrompt 拉 host 历史经验 + 全局业务规则 hint。
// limit 由 caller 提供（来自 cfg.Session.LessonsLimitInPrompt，默认 100）；
// lesson 与 hint 各取 top-N（按 priority desc）共用此 limit。
func loadKnowledgeForPrompt(ctx context.Context, store *lesson.Store, host string, limit int) string {
	if store == nil {
		return ""
	}
	if limit <= 0 {
		limit = 100
	}

	var b strings.Builder

	if host != "" {
		if lessons, err := store.ListByHost(ctx, host, limit); err == nil && len(lessons) > 0 {
			// 先过滤再 numbering——避免跳号（如全局 hint 混进 host lessons 时）。
			kept := make([]lesson.Lesson, 0, len(lessons))
			for _, l := range lessons {
				if l.Kind == lesson.KindHint && l.Host == lesson.HostGlobalHint {
					continue
				}
				kept = append(kept, l)
			}
			if len(kept) > 0 {
				b.WriteString("## Host 历史经验（distill 蒸馏，可能含旧情报；带具体 payload/手法可直接复用）\n\n")
				for i, l := range kept {
					fmt.Fprintf(&b, "%d. (priority=%d, hits=%d) %s\n", i+1, l.Priority, l.HitCount, l.Content)
				}
			}
		}
	}

	if hints, err := store.ListGlobalHints(ctx, limit); err == nil && len(hints) > 0 {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("## 跨 host 业务规则提醒（liusha 自定义判定逻辑，**必须遵守**——不是 OWASP 通用知识）\n\n")
		for i, h := range hints {
			fmt.Fprintf(&b, "%d. (priority=%d) %s\n", i+1, h.Priority, h.Content)
		}
	}

	return b.String()
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
