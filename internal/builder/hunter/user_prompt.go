package hunter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/notes"
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
	//   - RequestHeaders 非空或 URL 非空 → 渲染 raw HTTP 段（passive 父 / 带 flow_id 的子）
	//   - Brief 非空 → 渲染 brief 段（active 父 / 所有子）
	// 两者可并存：passive 父 spawn 子带 flow_id 时，子同时看到 raw HTTP + brief。
	if len(p.RequestHeaders) > 0 || p.URL != "" {
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
		// 段 3 (active 父独有) 或 段 1 (子任务无 flow): 自然语言 brief。
		// 子任务 brief 是父 LLM 写的指令；如父同时传 flow_id，本段位于流量段之后。
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		fmt.Fprintf(&b, "## 站点任务\n\n%s\n", p.Brief)
	}

	findingsLimit := deps.FindingsLimit
	if findingsLimit <= 0 {
		findingsLimit = 100
	}
	lessonsLimit := deps.LessonsLimit
	if lessonsLimit <= 0 {
		lessonsLimit = 100
	}

	// 段 3: 该 host 已有 finding（限本次 engagement，不跨次扫描）
	if existing := loadExistingFindings(ctx, deps.Findings, p.OwnerType, p.OwnerID, p.Host, findingsLimit); existing != "" {
		b.WriteString("\n\n## 该 host 已有 finding（本次扫描内）\n\n")
		b.WriteString(existing)
	}

	// 段 3.5: 本次扫描笔记板（engagement 内同 host 工作笔记）
	// 内容由其他 hunter task 通过 write_note 写入——临时凭据/状态、目标怪癖、小惊喜、失败死路。
	if notes := loadEngagementNotes(ctx, deps.Notes, p.OwnerID, p.Host); notes != "" {
		b.WriteString("\n\n## 本次扫描笔记板（engagement 内同 host）\n\n")
		b.WriteString(notes)
	}

	// 段 4: lesson + hint（跨 engagement 长期经验）
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
// tooling 与 vuln 各维护一份独立的 categoryOrder，buildCatalog 统一渲染。
type categoryItem struct {
	Key   string
	Label string
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

// buildToolingCatalog 渲染工具索引段——从 ToolsManifest（tools.yaml）按 category 分组渲染。
//
// 与 buildVulnCatalog 不同：vuln 仍扫 SKILL.md frontmatter（每个漏洞一个 SKILL，1:1 对应），
// 而 tooling 解耦——工具是否存在由 manifest（Dockerfile 同步）决定，SKILL.md 仅是可选详细手册。
//
// 输出形如：
//
//	## 可用外部工具索引
//
//	### recon（侦察 — 资产/服务/技术栈发现）
//	- **subfinder**: ...
//	- **httpx**: ...
//
// 用法约束（沙箱网络、read_tooling_skill 拉详细手册）在 hunter SKILL.md 里说，不在这段重复。
func buildToolingCatalog(m *manifest.Manifest) string {
	if m == nil || len(m.Tools) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## 可用外部工具索引\n")

	buckets := m.ByCategory()

	// 按 toolingCategoryOrder 固定顺序渲染——稳定 prompt 顺序，prompt cache 友好。
	for _, cat := range toolingCategoryOrder {
		group := buckets[cat.Key]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n", cat.Label)
		for _, t := range group {
			fmt.Fprintf(&b, "- **%s**: %s\n", t.Name, t.Description)
		}
		delete(buckets, cat.Key)
	}

	// 剩余 category 视作"未分类"——提醒维护者补 toolingCategoryOrder 或 tools.yaml category 字段。
	if len(buckets) > 0 {
		b.WriteString("\n### 未分类（建议补 tools.yaml category 或 toolingCategoryOrder）\n\n")
		rest := make([]string, 0, len(buckets))
		for k := range buckets {
			rest = append(rest, k)
		}
		sortStrings(rest)
		for _, k := range rest {
			for _, t := range buckets[k] {
				if k == "" {
					fmt.Fprintf(&b, "- **%s**: %s\n", t.Name, t.Description)
				} else {
					fmt.Fprintf(&b, "- **%s** (category=%s): %s\n", t.Name, k, t.Description)
				}
			}
		}
	}

	return b.String()
}

// buildCatalog 是 tooling / vuln 索引段的公共渲染逻辑：
// 按 Card.Category 分桶 → 按 order 固定顺序渲染已知分类 → 未匹配的落
// "未分类"组提醒维护者补 frontmatter。每组内 name 字典序；空组不渲染。
//
// Loader 为 nil 或 List() 为空时返回空串（不污染 prompt）。
// header 由调用方提供（含末尾换行），buildCatalog 不额外加分隔。
func buildCatalog(loader *skill.Loader, header string, order []categoryItem) string {
	if loader == nil {
		return ""
	}
	cards := loader.List()
	if len(cards) == 0 {
		return ""
	}

	// 按 Category 分桶。
	buckets := make(map[string][]*skill.Card, len(order)+1)
	for _, c := range cards {
		buckets[c.Category] = append(buckets[c.Category], c)
	}
	// 每桶内按 name 字典序——稳定 prompt 顺序，prompt cache 友好。
	for k := range buckets {
		sortCardsByName(buckets[k])
	}

	var b strings.Builder
	b.WriteString(header)

	// 按固定顺序渲染已知分类。
	for _, cat := range order {
		group := buckets[cat.Key]
		if len(group) == 0 {
			continue
		}
		fmt.Fprintf(&b, "\n### %s\n\n", cat.Label)
		for _, c := range group {
			fmt.Fprintf(&b, "- **%s**: %s\n", c.Name, c.Description)
		}
		delete(buckets, cat.Key)
	}

	// 剩余 category 视作"未分类"——提醒维护者补 frontmatter。
	if len(buckets) > 0 {
		b.WriteString("\n### 未分类（建议补 frontmatter category 字段）\n\n")
		rest := make([]string, 0, len(buckets))
		for k := range buckets {
			rest = append(rest, k)
		}
		sortStrings(rest)
		for _, k := range rest {
			for _, c := range buckets[k] {
				if k == "" {
					fmt.Fprintf(&b, "- **%s**: %s\n", c.Name, c.Description)
				} else {
					fmt.Fprintf(&b, "- **%s** (category=%s): %s\n", c.Name, k, c.Description)
				}
			}
		}
	}

	return b.String()
}

// buildVulnCatalog 渲染漏洞挖掘指南索引段（薄 wrapper —— 委托 buildCatalog）。
//
// 与 tooling 同模式：按 vulnCategoryOrder 分组渲染；当前阶段（web 主导）
// 只有一类，但分组结构与 tooling 保持一致，框架先立起来。
//
// 输出形如：
//
//	## 可用漏洞挖掘指南索引
//
//	### web（Web 应用漏洞）
//	- **bac**: 访问控制失效（Broken Access Control）...
//
// 用法约束（按 recon_checklist 判完方向、read_vuln_skill 拉详情）在 hunter SKILL.md 里说。
func buildVulnCatalog(loader *skill.Loader) string {
	header := "## 可用漏洞挖掘指南索引\n"
	body := buildCatalog(loader, header, vulnCategoryOrder)
	if body == "" {
		return ""
	}
	// 明确告诉 LLM 不要凭"行业常识"猜不在列表的 name——SKILL 库可能不完整。
	body += "\n> 列表外的漏洞类型**不要调** `read_vuln_skill`（会直接报错），凭工具知识 + sqlmap/dalfox/nuclei 等直接挖即可。\n"
	return body
}

// sortCardsByName 按 Name 字段对 *Card 切片做插入排序——切片小（每分类
// 1-5 条），插排开销可忽略，省一个 sort 包 import。
func sortCardsByName(cs []*skill.Card) {
	for i := 1; i < len(cs); i++ {
		for j := i; j > 0 && cs[j-1].Name > cs[j].Name; j-- {
			cs[j-1], cs[j] = cs[j], cs[j-1]
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

// loadExistingFindings 拉「engagement + host」已有 finding 摘要（dedup 参考）。
//
// 范围限 engagement+host，每次扫描独立，不被跨次扫描的历史污染（复用走 lesson，
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

// loadEngagementNotes 拉本次扫描 (engagement, host) 范围的 notes（短期工作内存）
// 渲染给 hunter user prompt。
//
// notes 按 (eid, host) 切分——本函数只读本 host 的笔记，不会混入其他 host 的
// 怪癖/死路。注入到 user prompt 让 hunter 看到同 (engagement, host) 内其他
// hunter task 写的笔记（临时凭据/状态、目标怪癖、小惊喜、失败死路），避免每个
// agent 从零摸索。
func loadEngagementNotes(ctx context.Context, store notes.Store, engagementID, host string) string {
	if store == nil || engagementID == "" || host == "" {
		return ""
	}
	raw, err := store.ReadNotes(ctx, engagementID, host)
	if err != nil || len(raw) == 0 {
		return ""
	}
	// raw 形如 {"notes": [{"content":"...","agent_run_id":"..."}, ...]}
	var parsed struct {
		Notes []struct {
			Content    string `json:"content"`
			AgentRunID string `json:"agent_run_id"`
		} `json:"notes"`
	}
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return ""
	}
	if len(parsed.Notes) == 0 {
		return ""
	}
	var b strings.Builder
	for _, n := range parsed.Notes {
		fmt.Fprintf(&b, "- %s\n", firstLine(n.Content, 200))
	}
	return b.String()
}

// loadKnowledgeForPrompt 拉 host 历史经验 + 全局业务规则 hint。
// limit 由 caller 提供（来自 cfg.Engagement.LessonsLimitInPrompt，默认 100）；
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
