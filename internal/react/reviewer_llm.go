package react

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/V3teran/liusha/internal/llm"
)

// StateReader 是 LLMReviewer 读取 engagement 三层 memory 的最小依赖。
// *engagement.Store 隐式满足该接口，测试可注入 stub。
type StateReader interface {
	ReadState(ctx context.Context, id string) ([]byte, error)
}

// LLMReviewer 用 light_provider LLM 在 ReAct 循环每 N 步做一次进度评估。
//
// 设计要点：
//   - 不阻塞主循环：LLM 错 / JSON 解析失败 / 非法 decision 一律回退 continue；
//   - 紧凑 prompt：当前流量摘要 + 最近窗口（含 ObsSummary）+ 状态板，≤ 几百 token；
//   - 严格 JSON 输出契约：`{"decision":"...","hint":"..."}`，三种合法值。
// fallback 截断阈值：caller 未注入对应字段时使用。
const (
	fallbackReviewerArgsTruncate = 80
	fallbackReviewerObsTruncate  = 120
)

type LLMReviewer struct {
	llm          llm.Generator
	state        StateReader
	engagementID string

	// 可选字段：caller 通常从 cfg.React.{ReviewerArgsTruncate, ReviewerObsTruncate} 注入。
	// 零值走 fallback 常量。
	ArgsTruncate int
	ObsTruncate  int

	// FlowSummary 是当前流量任务的一句话摘要（如 "GET vulnapp.local/api/user?id=1"），
	// 用于约束 reviewer 只评本流量进度，不要把 agent 推向其它流量任务。
	// 零值时 prompt 省略该段——退化为旧"无 flow 上下文"行为。
	FlowSummary string

	// FindingFetcher 是可选 hook：返回当前 agent_run 已写 finding 总数 + 最新一条概要（severity/summary）。
	// 由 scanner 装配处用 closure 适配 *finding.Store.CountAndLatestByAgentRun，避免本包反向 import finding 造成 cycle。
	// nil 时 reviewer prompt 不注入 finding 进度段——退化为"reviewer 看不到 finding 进展"行为。
	// 关键作用：让 reviewer 不再因为 ObsSummary 截断丢失 SUCCESS 关键字而误判"未挖到"。
	FindingFetcher func(ctx context.Context) (count int, latestSeverity, latestSummary string, err error)
}

func (o *LLMReviewer) effectiveArgsTruncate() int {
	if o.ArgsTruncate > 0 {
		return o.ArgsTruncate
	}
	return fallbackReviewerArgsTruncate
}

func (o *LLMReviewer) effectiveObsTruncate() int {
	if o.ObsTruncate > 0 {
		return o.ObsTruncate
	}
	return fallbackReviewerObsTruncate
}

// NewLLMReviewer 用 router.For("reviewer") 路由出的 light Generator + engagement store 构造。
//
// store 可为 nil（测试场景），此时 prompt 中省略 memory 状态板。
func NewLLMReviewer(g llm.Generator, store StateReader, engagementID string) *LLMReviewer {
	return &LLMReviewer{llm: g, state: store, engagementID: engagementID}
}

// reviewerSystemPrompt 约束 reviewer 只评本流量进度、输出严格 JSON。
const reviewerSystemPrompt = `你是漏洞挖掘主 agent 的进度评估者。基于"当前流量任务摘要 + 最近 N 步动作 + 已挖 finding 进度 + 状态板"评估进度，只评本流量任务，不要把 agent 推向其它流量或别的 host。

只能从以下三种 decision 中选一个：
- "continue"：进度正常或主漏洞已落库进入收尾，继续当前路径。
- "redirect"：当前路径有偏差，给一句 ≤ 100 字的中文方向提示（漏洞类型 / 验证阶段 / 验证手法层级，**禁止点具体工具名**，写到 hint 字段）。
- "terminate"：当前流量任务已挖完或反复卡死无进展，建议主 agent 立即调 done()。

判定规则：
- "已挖 finding 数 ≥ 1" 是**强信号**：该流量任务已有落库证据，**禁止**发"尚未挖到 / 改换方向"的 redirect hint；通常应 continue 让 agent 写 lesson + done，或 terminate 收手。
- 仅在 "已挖 finding 数 = 0" 且窗口里看到方向跑偏（如目标流量是 brute 却在做 SQLi）时才 redirect。
- 最近 1 步可能含完整工具输出（不截断）—— 关键字（SUCCESS / vulnerable / uid= / 反射 payload 完整回显）出现即视为命中，即便 finding 还没落库也别催换向。

严格只输出一个 JSON 对象，禁止任何多余文本：
{"decision":"continue|redirect|terminate","hint":"..."}`

// reviewerDecision 是 LLM 必须返回的 JSON 结构。
type reviewerDecision struct {
	Decision string `json:"decision"`
	Hint     string `json:"hint"`
}

// Evaluate 实现 Reviewer。
//
// 任何失败路径（store 读失败 / LLM 调用失败 / JSON 解析失败 / decision 非法）
// 都返回 keep_going，避免阻塞主循环——失败本身已写 warn 日志。
func (o *LLMReviewer) Evaluate(ctx context.Context, window []StepRecord) Verdict {
	user := buildReviewerPrompt(
		o.FlowSummary,
		window,
		o.readStateOrNil(ctx),
		o.fetchFindingsSection(ctx),
		o.effectiveArgsTruncate(),
		o.effectiveObsTruncate(),
	)

	res, err := o.llm.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: reviewerSystemPrompt},
		{Role: llm.RoleUser, Content: user},
	}, nil)
	if err != nil {
		slog.Warn("reviewer llm call failed", "err", err, "engagement_id", o.engagementID)
		return Verdict{Decision: VerdictContinue}
	}

	var dec reviewerDecision
	content := strings.TrimSpace(res.Content)
	if err := json.Unmarshal([]byte(content), &dec); err != nil {
		slog.Warn("reviewer parse json failed", "raw", content, "engagement_id", o.engagementID)
		return Verdict{Decision: VerdictContinue}
	}

	if v := normalizeDecision(dec.Decision); v != "" {
		return Verdict{Decision: v, Hint: dec.Hint}
	}
	slog.Warn("reviewer unknown decision", "decision", dec.Decision, "engagement_id", o.engagementID)
	return Verdict{Decision: VerdictContinue}
}

// normalizeDecision 把 LLM 输出的 decision 字段归一化到 3 种合法值。
//
// 设计原因：LLM 偶发 typo / 大小写差异 / 含连字符或下划线变体会被 strict switch
// 直接拒绝。这里用宽松匹配吸收常见变体，避免噪音日志：
//   - 包含 "terminate" / "abort" / "stop"   → terminate
//   - 包含 "redirect"  / "steer"  / "adjust" → redirect
//   - 包含 "continue"  / "keep"   / "go"     → continue
//
// 完全无法识别返回 ""，让 caller 仍 fallback continue + warn（保留可观测性）。
func normalizeDecision(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(s, "terminate"), strings.Contains(s, "abort"), strings.Contains(s, "stop"):
		return VerdictTerminate
	case strings.Contains(s, "redirect"), strings.Contains(s, "steer"), strings.Contains(s, "adjust"):
		return VerdictRedirect
	case strings.Contains(s, "continue"), strings.Contains(s, "keep"), strings.Contains(s, "go"):
		return VerdictContinue
	}
	return ""
}

// readStateOrNil 读三层 memory；失败或 store nil 时返回 nil 让 prompt 省略状态板段。
func (o *LLMReviewer) readStateOrNil(ctx context.Context) []byte {
	if o.state == nil {
		return nil
	}
	data, err := o.state.ReadState(ctx, o.engagementID)
	if err != nil {
		slog.Warn("reviewer read state failed", "err", err, "engagement_id", o.engagementID)
		return nil
	}
	return data
}

// fetchFindingsSection 调 FindingFetcher 拿当前 agent_run 已写 finding 数 + 最新一条概要，
// 渲染成 prompt 段；nil hook / 0 finding / 错误 → 返回空串让 buildReviewerPrompt 省略段头。
//
// 渲染示例：
//
//	已挖 finding 数：2（最新 [critical] SQL Injection in /vulnerabilities/sqli/ ...）
func (o *LLMReviewer) fetchFindingsSection(ctx context.Context) string {
	if o.FindingFetcher == nil {
		return ""
	}
	count, sev, sum, err := o.FindingFetcher(ctx)
	if err != nil {
		slog.Warn("reviewer fetch findings failed", "err", err, "engagement_id", o.engagementID)
		return ""
	}
	if count == 0 {
		return "已挖 finding 数：0（尚未落库）"
	}
	if sev == "" && sum == "" {
		return fmt.Sprintf("已挖 finding 数：%d", count)
	}
	return fmt.Sprintf("已挖 finding 数：%d（最新 [%s] %s）", count, sev, truncate(sum, 200))
}

// buildReviewerPrompt 把 flow 摘要 + window + state + findings 段拼成单条 user 消息。
//
//   - flowSummary 为空时省略段头（向后兼容旧调用方）；
//   - window 为空时仍能产出 prompt（空 window 段）；
//   - state 为 nil 时省略状态板段；
//   - findingsSection 为空时省略 finding 进度段（FindingFetcher nil / 出错 / 0 finding 都可能为空）；
//   - argsTruncate / obsTruncate 来自 LLMReviewer 的可选字段，控制喂 LLM 的字节数；
//   - 最近 1 步（window 末尾）若 FullObs 非空，用 FullObs 整段（不截断）替代 ObsSummary，
//     让 reviewer 看到 SUCCESS/vulnerable/uid= 等关键字防止摘要丢失误判。
func buildReviewerPrompt(flowSummary string, window []StepRecord, state []byte, findingsSection string, argsTruncate, obsTruncate int) string {
	var b strings.Builder
	if flowSummary != "" {
		fmt.Fprintf(&b, "当前流量任务：%s\n\n", flowSummary)
	}
	fmt.Fprintf(&b, "最近 %d 步动作：\n", len(window))
	lastIdx := len(window) - 1
	for i, w := range window {
		obs := truncate(w.ObsSummary, obsTruncate)
		if i == lastIdx && w.FullObs != "" {
			obs = w.FullObs // 末尾一步给完整观测，防止关键字被截断
		}
		fmt.Fprintf(&b, "%d. %s(%s) → %s\n", i+1, w.ActionName, truncate(string(w.Args), argsTruncate), obs)
	}
	if findingsSection != "" {
		b.WriteString("\n")
		b.WriteString(findingsSection)
		b.WriteString("\n")
	}
	if len(state) > 0 {
		b.WriteString("\n当前状态板（facts/ideas/hints）：\n")
		b.Write(state)
	}
	b.WriteString("\n\n请按 system 约束的 JSON 输出。")
	return b.String()
}

// truncate 把超长字段裁剪到 max（按字节）并加省略号，避免 prompt 膨胀。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
