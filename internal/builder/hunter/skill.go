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
	"github.com/V3teran/liusha/internal/toolruntime"
	"github.com/V3teran/liusha/internal/toolruntime/middleware"
	"github.com/V3teran/liusha/internal/tools/common"
	"github.com/V3teran/liusha/internal/tools/external"
	"github.com/V3teran/liusha/internal/tools/runners"
)

// Deps hunter builder 的依赖注入。由 cmd/scanner/main.go 在启动时构造一份。
type Deps struct {
	Engagements *engagement.Store
	Findings    *finding.Store
	Lessons     *lesson.Store
	Credentials credential.Provider
	SkillLoader *skill.Loader

	// ToolingLoader root 指向 skills/tooling/，给 read_tooling_skill 用 +
	// buildUserPrompt 启动期扫 frontmatter 注入"工具索引"段（Progressive
	// Disclosure：常驻索引省 token，详情按需 read_tooling_skill 拉）。
	// nil 时不注入索引段、不注册 read_tooling_skill 工具（向后兼容）。
	ToolingLoader *skill.Loader

	// 容器化沙箱执行器（run_command 工具的运行时）。
	DockerRunner  *runners.DockerRunner // nil 时 run_command 不注册
	PentoolsImage string                // 默认 liusha/pentools:1.0.0
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
	b.WriteString("## 流量请求\n\n```\n")
	b.WriteString(strings.ToUpper(p.Method))
	b.WriteString(" ")
	b.WriteString(p.URL)
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
	// 列出沙箱内所有可调外部 CLI 工具的 name + 一句话用途。
	// 详情按需调 read_tooling_skill(name) 拉，不在 prompt 常驻。
	if catalog := buildToolingCatalog(deps.ToolingLoader); catalog != "" {
		b.WriteString("\n\n")
		b.WriteString(catalog)
	}

	// 段 5: 行动指令
	b.WriteString("\n\n→ 找出这条流量涉及的所有漏洞，用 `write_finding(...)` 入库；完成或确认无漏洞调 `done()`。")

	return b.String()
}

// buildToolingCatalog 从 ToolingLoader 拉所有已 Index 的工具 frontmatter，
// 拼成 markdown 索引段。Loader 为 nil 或无工具时返回空串（不污染 prompt）。
//
// 输出形如：
//
//	## 可用外部工具（沙箱内预装）
//
//	需要详细用法时调 `read_tooling_skill(name="<name>")` 拉完整手册。
//
//	- **sqlmap**: SQL 注入自动探测/利用——...
//	- **curl**: 原生 HTTP 客户端——...
func buildToolingCatalog(loader *skill.Loader) string {
	if loader == nil {
		return ""
	}
	cards := loader.List()
	if len(cards) == 0 {
		return ""
	}
	// 按 name 字典序排序——稳定 prompt 顺序，prompt 缓存命中率更高。
	sortedCards := make([]*skill.Card, 0, len(cards))
	sortedCards = append(sortedCards, cards...)
	for i := 1; i < len(sortedCards); i++ {
		for j := i; j > 0 && sortedCards[j-1].Name > sortedCards[j].Name; j-- {
			sortedCards[j-1], sortedCards[j] = sortedCards[j], sortedCards[j-1]
		}
	}

	var b strings.Builder
	b.WriteString("## 可用外部工具（沙箱内预装；run_command 调用）\n\n")
	b.WriteString("**沙箱网络约束**：容器内 `127.0.0.1` / `localhost` = 容器自己，**不是宿主**。" +
		"调外部工具访问流量里的 host 时，把 url 里的 `127.0.0.1` / `localhost` 替换为 `host.docker.internal`" +
		"（已注入容器 hosts；写 finding 时仍用原 url 标真实坐标）。\n\n")
	b.WriteString("需要详细用法时调 `read_tooling_skill(name=\"<name>\")` 拉完整手册（环境约束 / 项目策略 / 写 finding 红线 / 决策边界）。\n\n")
	for _, c := range sortedCards {
		fmt.Fprintf(&b, "- **%s**: %s\n", c.Name, c.Description)
	}
	return b.String()
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
