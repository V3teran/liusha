// Package hunter 是 v0024 agentic-lean 的唯一 ReAct skill builder：
// scanner 拉到 flow 后调 NewBuilder(deps)(ctx, params) 拿 react.Config 跑 react.Run。
//
// 单层架构：无 orchestrator 主层 / sub-react 子层。hunter agent 接到一条流量
// （request + response + 凭证 + 已有 finding + hint），自由组合 7 个工具
// （run_command / read_memory / write_memory / credentials / findings / finding / done）
// 挖出该流量涉及的所有漏洞。
package hunter

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/config"
	"github.com/V3teran/liusha/internal/credential"
	"github.com/V3teran/liusha/internal/engagement"
	"github.com/V3teran/liusha/internal/finding"
	"github.com/V3teran/liusha/internal/lesson"
	"github.com/V3teran/liusha/internal/react"
	"github.com/V3teran/liusha/internal/skill"
	toolfx "github.com/V3teran/liusha/internal/toolruntime"
	"github.com/V3teran/liusha/internal/toolruntime/middleware"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/external"
	"github.com/V3teran/liusha/internal/tools/manifest"
	"github.com/V3teran/liusha/internal/tools/runners"
)

// Deps hunter builder 的依赖注入。由 cmd/scanner/main.go 在启动时构造一份。
type Deps struct {
	Engagements *engagement.Store
	Findings    *finding.Store
	Lessons     *lesson.Store
	Credentials credential.Provider
	SkillLoader *skill.Loader

	// ToolingLoader root 指向 skills/tooling/，给 read_tooling_skill 工具用（按需读 SKILL.md 详细手册）。
	// nil 时不注册 read_tooling_skill 工具（向后兼容）。
	// 注意：工具索引段（Tier 1）不再扫 SKILL.md frontmatter 拼，而是读 ToolsManifest——
	// 这样"工具是否存在"与"工具是否有 SKILL 详细手册"解耦：删 SKILL 不等于工具消失。
	ToolingLoader *skill.Loader

	// ToolsManifest 是 deployments/tool-images/pentools/tools.yaml 解析后的清单——
	// 与 Dockerfile 装的 binary 严格对应，hunter 用它渲染 SystemPrompt 的 tooling_catalog 段。
	// nil 时不注入索引段（LLM 看不到沙箱有哪些工具，应在 scanner 启动期 fail-fast）。
	ToolsManifest *manifest.Manifest

	// VulnLoader root 指向 skills/vuln/，给 read_vuln_skill 用 +
	// buildUserPrompt 启动期扫 frontmatter 注入"漏洞挖掘指南索引"段。
	// 与 ToolingLoader 同模式：常驻极简索引（无 category 分组），详情按需
	// read_vuln_skill 拉。nil 时不注入索引段、不注册 read_vuln_skill 工具。
	VulnLoader *skill.Loader

	// 容器化沙箱执行器（run_command 工具的运行时）。
	DockerRunner  *runners.DockerRunner // nil 时 run_command 不注册
	PentoolsImage string                // 默认 liusha/pentools:latest
	ScanNetwork   string                // 默认空（docker bridge）

	SandboxCfg config.SandboxConfig

	// Tenant 用于按 (tenant, host) 拉 lesson + (tenant, '*', kind=hint) 拉全局规则。
	Tenant string

	// ToolExecuteTimeoutSeconds 单次 tool Execute 兜底超时（秒）；0 = 不加 deadline。
	ToolExecuteTimeoutSeconds int

	// 预算
	MaxSteps            int
	WatchdogSeconds     int
	ObserverEverySteps  int
	DoneForceMaxRejects int

	// Prompt 拼装预算
	UserPromptBodyLimit int // 请求/响应 body 单段截断字节数；≤0 → 8192
	HintsLimit          int // 注入 user prompt 的 hint 条数；≤0 → 20
}

const skillName = "hunter"

// NewBuilder 构造 hunter SkillBuilder 闭包。scanner 启动时调用一次。
func NewBuilder(deps Deps) skill.Builder {
	return func(ctx context.Context, p skill.BuilderParams) (react.Config, error) {
		reg := toolfx.NewRegistry()

		_ = reg.Register(&common.ReadMemory{Store: deps.Engagements, EngagementID: p.EngagementID, TaskID: p.TaskID})
		_ = reg.Register(&common.WriteMemory{Store: deps.Engagements, EngagementID: p.EngagementID, TaskID: p.TaskID})
		_ = reg.Register(&common.ReadCredentials{Provider: deps.Credentials, Host: p.Host})
		_ = reg.Register(&common.ReadFindings{Store: deps.Findings, Host: p.Host})
		_ = reg.Register(&common.WriteFinding{
			Store:        deps.Findings,
			EngagementID: p.EngagementID,
			TaskID:       p.TaskID,
			Host:         p.Host,
			FlowID:       p.FlowID,
		})
		_ = reg.Register(&common.UpdateFinding{Store: deps.Findings})
		_ = reg.Register(&common.ReadRelations{Store: deps.Findings, EngagementID: p.EngagementID})
		_ = reg.Register(&common.WriteRelation{Store: deps.Findings})
		_ = reg.Register(&common.ReadLessons{Store: deps.Lessons, Tenant: deps.Tenant, Host: p.Host})
		_ = reg.Register(&common.WriteLesson{Store: deps.Lessons, Tenant: deps.Tenant, Host: p.Host})
		_ = reg.Register(common.Done{})

		// Progressive Disclosure Tier 2：LLM 看 user prompt 工具索引选中工具后
		// 调本工具拿完整 SKILL.md。Loader 由 cmd/scanner 单独装配（root=skills/tooling）。
		if deps.ToolingLoader != nil {
			_ = reg.Register(&common.ReadToolingSkill{Loader: deps.ToolingLoader})
		}

		// Progressive Disclosure Tier 2（漏洞挖掘指南）：LLM 按 recon_checklist
		// 判完流量方向后，调本工具拿对应漏洞类型完整 SKILL.md。
		// Loader root=skills/vuln，由 cmd/scanner 单独装配。
		if deps.VulnLoader != nil {
			_ = reg.Register(&common.ReadVulnSkill{Loader: deps.VulnLoader})
		}

		if deps.DockerRunner != nil {
			s := deps.SandboxCfg
			_ = reg.Register(&external.RunCommand{
				Runner:         deps.DockerRunner,
				Image:          deps.PentoolsImage,
				Network:        deps.ScanNetwork,
				MinTimeout:     time.Duration(s.RunMinTimeoutSeconds) * time.Second,
				MaxTimeout:     time.Duration(s.RunMaxTimeoutSeconds) * time.Second,
				DefaultTimeout: time.Duration(s.RunDefaultTimeoutSeconds) * time.Second,
				DefaultMemMB:   s.RunDefaultMemMB,
				DefaultCPUs:    s.RunDefaultCPUs,
				TailBytes:      s.RunTailBytes,
			})
		}

		card, err := deps.SkillLoader.Load(skillName)
		if err != nil {
			return react.Config{}, fmt.Errorf("load skill %q: %w", skillName, err)
		}

		// agentic-lean：删除 done validator——不强制结构化 done.reason，agent 自由收手
		reg.Use(
			middleware.Observe(),
			middleware.Timeout(deps.ToolExecuteTimeoutSeconds),
			middleware.ResultCompress(0, 0, 0),
		)

		userPrompt := buildUserPrompt(ctx, deps, p)

		maxSteps := deps.MaxSteps
		if maxSteps <= 0 {
			maxSteps = 30
		}
		watchdog := deps.WatchdogSeconds
		if watchdog <= 0 {
			watchdog = 60
		}

		return react.Config{
			LLM:                 p.LLM,
			Actions:             reg,
			Budget:              react.Budget{MaxSteps: maxSteps, WatchdogSeconds: watchdog},
			SystemPrompt:        card.Body,
			UserPrompt:          userPrompt,
			Observer:            p.Observer,
			ObserverEverySteps:  deps.ObserverEverySteps,
			DoneForceMaxRejects: deps.DoneForceMaxRejects,
		}, nil
	}
}

// buildUserPrompt 拼接 hunter agent 的第一条 user message：
// 流量请求 + 流量响应 + 该 host 已有 finding + lesson/hint + 行动指令。
func buildUserPrompt(ctx context.Context, deps Deps, p skill.BuilderParams) string {
	bodyLimit := deps.UserPromptBodyLimit
	if bodyLimit <= 0 {
		bodyLimit = 8192
	}

	var b strings.Builder

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

	// 段 3: 该 host 已有 finding
	if existing := loadExistingFindings(ctx, deps.Findings, p.Host); existing != "" {
		b.WriteString("\n\n## 该 host 已有 finding（写新 finding 前先看，别重复）\n\n")
		b.WriteString(existing)
	}

	// 段 4: lesson + hint
	if knowledge := loadKnowledgeForPrompt(ctx, deps.Lessons, deps.Tenant, p.Host, deps.HintsLimit); knowledge != "" {
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

	// 段 5: 行动指令
	b.WriteString("\n\n→ 找出这条流量涉及的所有漏洞，用 `write_finding(...)` 入库；完成或确认无漏洞调 `done()`。")

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
//	## 可用外部工具（沙箱内预装；run_command 调用）
//	**沙箱网络约束**：...
//	需要详细用法时调 read_tooling_skill(name="<name>") 拉完整手册（仅复杂工具有 SKILL）。
//
//	### recon（侦察 — 资产/服务/技术栈发现）
//	- **subfinder**: ...
//	- **httpx**: ...
func buildToolingCatalog(m *manifest.Manifest) string {
	if m == nil || len(m.Tools) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("## 可用外部工具（沙箱内预装；run_command 调用）\n\n")
	b.WriteString("**沙箱网络约束**：容器走 bridge 出网，访问目标时 url 直接用流量里的真实 host:port\n")
	b.WriteString("需要详细用法时调 `read_tooling_skill(name=\"<name>\")` 拉完整手册（仅复杂工具有 SKILL，简单工具靠 `--help` 即可）。\n")

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
//	## 可用漏洞挖掘指南（按 recon_checklist 判完方向后，read_vuln_skill 拉详细）
//
//	### web（Web 应用漏洞）
//	- **bac**: 访问控制失效（Broken Access Control）...
func buildVulnCatalog(loader *skill.Loader) string {
	header := "## 可用漏洞挖掘指南（按 recon_checklist 判完方向后，read_vuln_skill 拉详细）\n"
	return buildCatalog(loader, header, vulnCategoryOrder)
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

// loadExistingFindings 拉 host 已有 finding 摘要（dedup 参考）；最多 20 条。
func loadExistingFindings(ctx context.Context, store *finding.Store, host string) string {
	if store == nil || host == "" {
		return ""
	}
	fs, err := store.ListByHost(ctx, host)
	if err != nil || len(fs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, f := range fs {
		if i >= 20 {
			fmt.Fprintf(&b, "...（还有 %d 条；用 findings() 工具查全）\n", len(fs)-i)
			break
		}
		fmt.Fprintf(&b, "- [%s] %s\n", f.Severity, firstLine(f.Summary, 120))
	}
	return b.String()
}

// loadKnowledgeForPrompt 拉 host 历史经验 + 全局业务规则 hint。
func loadKnowledgeForPrompt(ctx context.Context, store *lesson.Store, tenant, host string, limit int) string {
	if store == nil {
		return ""
	}
	if limit <= 0 {
		limit = 20
	}

	var b strings.Builder

	if host != "" {
		if lessons, err := store.ListByHost(ctx, tenant, host, limit); err == nil && len(lessons) > 0 {
			b.WriteString("## Host 历史经验（distill 蒸馏，可能含旧情报；带具体 payload/手法可直接复用）\n\n")
			for i, l := range lessons {
				if l.Kind == lesson.KindHint && l.Host == lesson.HostGlobalHint {
					continue
				}
				fmt.Fprintf(&b, "%d. (priority=%d, hits=%d) %s\n", i+1, l.Priority, l.HitCount, l.Content)
			}
		}
	}

	if hints, err := store.ListGlobalHints(ctx, tenant, limit); err == nil && len(hints) > 0 {
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
