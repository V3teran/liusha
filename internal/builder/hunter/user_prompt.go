package hunter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lead"
	"github.com/V3teran/liusha/internal/skill"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/traffic"
)

// trafficPreviewBodyLimit 是批流量清单里每条 req/resp body 的预览截断上限。
// passive 一批默认 20 条，若全渲染完整 body（各 32 KiB）最坏 ~1.28 MB / ~50 万 token 撑爆 prompt；
// 各留头 2 KiB → 20 条 ×~4 KiB ≈ 80 KiB / 几万 token，稳。要看完整 body 调 view_traffic 按需拉。
const trafficPreviewBodyLimit = 2048

func buildUserPrompt(ctx context.Context, deps Deps, p skill.BuilderParams) string {
	bodyLimit := deps.UserPromptBodyLimit
	if bodyLimit <= 0 {
		bodyLimit = 8192
	}

	var b strings.Builder

	// passive 批分析：本 task 认领的整批 proxy_traffic 全量渲染成清单（摘要 + body 预览）——
	// agent 开箱即见全部流量，不必靠 list_traffic 发现（消灭「空手/幻觉 host」翻车）。
	if len(p.Traffic) > 0 {
		writeTrafficBatch(&b, p.Traffic)
	}

	// 字段触发渲染（不再 Mode-driven）：
	//   - RequestHeaders 非空或 URL 非空 → 渲染 raw HTTP 段（trafficAnalysis / 带 traffic_id 的 exploitation）
	//   - Brief 非空 → 渲染 brief 段（orchestrator / exploitation）
	// 两者可并存：trafficAnalysis spawn exploitation 带 traffic_id 时，exploitation 同时看到 raw HTTP + brief。
	if len(p.RequestHeaders) > 0 || p.URL != "" {
		// 段 0（0060+ 流量字典）：本流量已入 agent_traffic 表，告诉 LLM traffic_id 让它能用
		// replay_traffic(id=N, modifications={...}) 改参数重发——比手写 curl 准 100 倍，
		// 自动继承 cookie/CSRF/auth header/其它 form 字段。
		if p.TrafficID > 0 {
			fmt.Fprintf(&b, "## 当前流量\n\n本流量 `traffic_id=%d`。复用此请求改某参数 fuzz / IDOR / 注 payload，"+
				"调 `replay_traffic(id=%d, modifications={...})`，工具自动继承所有 header / cookie / form 字段。\n\n",
				p.TrafficID, p.TrafficID)
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
		// 段 3 (orchestrator 独有) 或 段 1 (exploitation 无 traffic): 自然语言 brief。
		// exploitation brief 是 orchestrator LLM 写的指令；orchestrator 同时传 traffic_id 时，本段位于流量段之后。
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

	findingsLimit := p.FindingsLimit // handler 从 settingstore(runtime) 现读注入；≤0 兜底 100
	if findingsLimit <= 0 {
		findingsLimit = 100
	}

	// 段 3: 该 host 已有 finding（限本次 owner，不跨次扫描）
	if existing := loadExistingFindings(ctx, deps.Findings, p.TaskID, p.Host, findingsLimit); existing != "" {
		b.WriteString("\n\n## 该 host 已有 finding（本次扫描内）\n\n")
		b.WriteString(existing)
	}

	// notes 笔记板段已退役——agent 思路改输出到会话（reasoning），跨 task 上下文走会话历史。
	// 跨目标长期知识（原 lesson 段）已改为 corpus PULL——agent 用 search_corpus 按需检索，
	// 不再 PUSH 全量注入（大库无差别注入是噪音，见 lesson→corpus 重构设计）。

	// 段 4: 情报黑板（lead，§7）——顶层 agent 只读注入，无工具（与子代理经
	// leadSection 拼进 Instruction 同源，但顶层走 user prompt 而非 system prompt）。
	if leadText := loadLeadForPrompt(ctx, deps.Lead, p.Host); leadText != "" {
		b.WriteString("\n\n")
		b.WriteString(leadText)
	}

	// 段 4.5: Tier 1 工具索引（Progressive Disclosure）——
	// 列出沙箱内所有可调外部 CLI 工具的 name + 一句话用途，来源 tools.yaml（与 Dockerfile 同步）。
	// 详情按需调 read_tooling_skill(name) 拉 SKILL.md，不在 prompt 常驻。
	if catalog := buildToolingCatalog(deps.ToolsManifest, p.CliTools); catalog != "" {
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

// toolingCategoryOrder 是工具索引段的固定渲染顺序——沿 PTES 流水线延伸覆盖六域能力轴：
// web 侦察→发现→漏扫→注入 … → binary（reverse/pwn）→ cloud（cloud/container）
// → 渗透（exploitation/post-exploit）→ CTF（crypto/forensics/stego/cracking）→ 支撑轴（runtime/browser/utility）。
// 顺序固定让 prompt cache 命中率最高（同一批工具集 → 同一 prefix）。
// 未在本表内的 category（含空值）→ 落入末尾的"未分类"组，提醒维护者补 frontmatter。
var toolingCategoryOrder = []categoryItem{
	{"recon", "recon（侦察 — 资产/服务/技术栈/指纹发现）"},
	{"discovery", "discovery（内容/参数发现）"},
	{"vulnscan", "vulnscan（自动化模板漏扫）"},
	{"injection", "injection（注入类专项）"},
	{"deserialization", "deserialization（反序列化 payload 生成）"},
	{"auth", "auth（认证/凭证攻击）"},
	{"oob", "oob（带外回调检测 — Blind SSRF/RCE/XXE/XSS）"},
	{"sast", "sast（源码静态分析）"},
	{"reverse", "reverse（二进制静态逆向 — 反汇编/反编译/结构修补）"},
	{"pwn", "pwn（二进制动态利用 — 调试/ROP/符号执行/模糊测试）"},
	{"cloud", "cloud（云平台攻击 — IAM/配置/枚举，AWS 向）"},
	{"container", "container（容器/K8s 攻击 — 镜像扫描/集群横移/基线）"},
	{"exploitation", "exploitation（拿初始 shell — 利用框架/协议攻击/中继）"},
	{"post-exploit", "post-exploit（后渗透 — 横向移动/AD 攻击/隧道代理）"},
	{"crypto", "crypto（密码学攻击/分析）"},
	{"forensics", "forensics（取证 — 内存/磁盘/流量/固件）"},
	{"stego", "stego（隐写术）"},
	{"cracking", "cracking（哈希/密码破解）"},
	{"runtime", "runtime（语言运行时/编译器 — 现场编写/编译/运行 payload）"},
	{"browser", "browser（无头浏览器自动化）"},
	{"utility", "utility（通用胶水：HTTP/JSON/脚本）"},
}

// vulnCategoryOrder 是漏洞挖掘指南索引段的固定渲染顺序——
// 按"领域 + 形态"切分；未来扩展到 cloud/container/post-exploit 时在此追加 key。
// 当前阶段（web 主导）只有一类，但分组结构与 tooling 保持一致，框架先立起来。
var vulnCategoryOrder = []categoryItem{
	{"web", "web（Web 应用漏洞）"},
}

// buildToolingCatalog 渲染工具索引段——数据来自 ToolsManifest（tools.yaml，与 Dockerfile 同步）。
// 与 vuln 不同：工具是否存在由 manifest 决定，SKILL.md 仅是可选详细手册（按需 read_tooling_skill 拉）。
// 按本猎手的 cliTools 严格白名单经 FilterByNames 过滤：cliTools 空 = 空集，不装配任何外部工具。
func buildToolingCatalog(m *manifest.Manifest, cliTools []string) string {
	if m == nil || len(m.Tools) == 0 {
		return ""
	}
	m = m.FilterByNames(cliTools)
	if len(m.Tools) == 0 {
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

// writeTrafficBatch 全量渲染 passive 本批认领流量：概览表（method/path/status 一眼扫）+ 每条明细
// （headers + body 预览，各截 trafficPreviewBodyLimit）。agent 开箱即见本批全部流量，不必靠
// list_traffic 发现；某条 body 被截、需看全文时才 view_traffic(id) 按需拉。
func writeTrafficBatch(b *strings.Builder, trafficList []traffic.ProxyTraffic) {
	host := ""
	if len(trafficList) > 0 {
		host = trafficList[0].Host
	}
	fmt.Fprintf(b, "## 本批待分析流量（%d 条，host=%s）\n\n", len(trafficList), host)
	b.WriteString("下方已列出本次分析的全部流量，无需再调 `list_traffic` 发现。" +
		"每条含 method/path/status + 请求/响应头 + body 预览；body 被截断需看全文时调 `view_traffic(id)`。\n\n")

	// 概览表：让 agent 先一眼扫全批，再看明细。
	b.WriteString("### 概览\n\n| id | method | path | status |\n|---|---|---|---|\n")
	for _, f := range trafficList {
		fmt.Fprintf(b, "| %d | %s | %s | %d |\n", f.ID, f.Method, f.Path, f.StatusCode)
	}

	// 明细：逐条完整请求/响应报文（raw 单一源，v0100+），各截 trafficPreviewBodyLimit。
	for _, f := range trafficList {
		fmt.Fprintf(b, "\n### 流量 #%d — %s %s → %d\n\n", f.ID, f.Method, f.Path, f.StatusCode)
		b.WriteString("请求报文：")
		writeBodyBlock(b, f.RequestRaw, trafficPreviewBodyLimit)
		b.WriteString("\n响应报文：")
		writeBodyBlock(b, f.ResponseRaw, trafficPreviewBodyLimit)
	}
	b.WriteString("\n")
}

// loadExistingFindings 拉「owner + host」已有 finding 摘要（dedup 参考）。
//
// 范围限 owner+host，每次扫描独立，不被跨次扫描的历史污染（跨目标复用走 corpus 检索，
// 由 loadKnowledgeForPrompt 注入段 4）。
// readLimit 仅作 DB 读上限的安全闸（取够高，正常扫描不触及）——读到的 finding **全量注入**，
// 不在此 top-N 截断/压缩；prompt 超长由 ① summarization 统一压缩，agent 仍可 read_findings 取全。
//
// 按 (task_id, host) 查 finding（合表后单一 task 维度）。taskID 或 host 为空则跳过。
func loadExistingFindings(ctx context.Context, store *finding.Store, taskID, host string, readLimit int) string {
	if store == nil || taskID == "" || host == "" || readLimit <= 0 {
		return ""
	}
	fs, err := store.ListByTaskAndHost(ctx, taskID, host, readLimit)
	if err != nil || len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	for _, f := range fs {
		fmt.Fprintf(&b, "- [%s] %s\n", f.Severity, firstLine(f.Summary, 120))
	}
	return b.String()
}

// loadLeadForPrompt 拉该 host 的情报黑板（读时按 kind 分组去重，见 lead.FormatSection）。
// store nil / host 空 / 读取失败 / 无情报 → 返回空串，不污染 prompt。
func loadLeadForPrompt(ctx context.Context, store *lead.Store, host string) string {
	if store == nil || host == "" {
		return ""
	}
	grouped, err := store.ReadRecent(ctx, host)
	if err != nil {
		return ""
	}
	return lead.FormatSection(grouped)
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
