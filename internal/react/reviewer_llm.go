package react

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/V3teran/liusha/internal/llm"
)

// NotesReader 是 LLMReviewer 读取 engagement 共享笔记板的最小依赖。
// *notes.RedisStore 隐式满足该接口（internal/notes 包），测试可注入 stub。
type NotesReader interface {
	ReadNotes(ctx context.Context, id string) ([]byte, error)
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
	notes        NotesReader
	engagementID string

	// 可选字段：caller 通常从 cfg.React.{ReviewerArgsTruncate, ReviewerObsTruncate} 注入。
	// 零值走 fallback 常量。
	ArgsTruncate int
	ObsTruncate  int

	// FlowSummary 是当前流量任务的一句话摘要（如 "GET vulnapp.local/api/user?id=1"），
	// 用于约束 reviewer 只评本流量进度，不要把 agent 推向其它流量任务。
	// 零值时 prompt 省略该段——退化为旧"无 flow 上下文"行为。
	FlowSummary string

	// HostFindingsFetcher 是可选 hook：返回当前 engagement + host 范围内已有 finding 列表
	// （每条形如 "[severity] summary"，前 N 条）。由 scanner 装配处用 closure 适配
	// *finding.Store.ListByEngagementAndHost。nil 时 reviewer prompt 不注入 finding 段。
	//
	// 关键设计：列表**仅作背景参考**，不返回 count、不参与 terminate 判定。
	// reviewer 判定 terminate/redirect/continue 必须基于 window 行为本身——
	// 避免历史误判（"host 已有 finding 数 ≥ 1" 把别的流量战果误算成本任务进度，
	// 然后催 done，bac/profile 真无漏洞的流量被误推 terminate / 编造 hint）。
	HostFindingsFetcher func(ctx context.Context) ([]string, error)

	// LessonFetcher 是可选 hook：返回该 host 历史 lesson（跨 engagement 长期经验）。
	// 由 scanner 装配处用 closure 适配 *lesson.Store.ListByHost。
	// nil 时 reviewer prompt 不注入 lesson 段。
	// 关键作用：reviewer 用 lesson 给出更精准方向修正 hint；仅服务方向修正，不参与"是否 terminate"决策。
	LessonFetcher func(ctx context.Context) ([]string, error)
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

// NewLLMReviewer 用 router.For("reviewer") 路由出的 light Generator + notes store 构造。
//
// store 可为 nil（测试场景），此时 prompt 中省略笔记板段。
func NewLLMReviewer(g llm.Generator, store NotesReader, engagementID string) *LLMReviewer {
	return &LLMReviewer{llm: g, notes: store, engagementID: engagementID}
}

// reviewerSystemPrompt 约束 reviewer 只评本流量进度、输出严格 JSON。
//
// 设计原则：terminate / redirect / continue 判定完全基于 window 行为本身。
// host 已有 finding 列表 / lesson 仅作背景参考，**不**参与 terminate 计数。
// 一个流量任务可能挖出 0 / 1 / 多个 finding，没有数字阈值能直接推 done。
const reviewerSystemPrompt = `你是漏洞挖掘主 agent 的进度评估者。基于"当前流量任务摘要 + 最近 N 步动作 + 笔记 / lesson / host 已有 finding（背景参考）"评估进度，只评本流量任务，不要把 agent 推向其它流量或别的 host。

只能从以下三种 decision 中选一个：
- "continue"：默认值。进度正常 / 命中后正常扩展 / 正在合理探索 → 一律 continue 信任主 agent 自主推进。
- "redirect"：当前路径有偏差或命中未落库，给一句 ≤ 100 字的中文方向提示（漏洞类型 / 验证阶段 / 验证手法层级，**禁止点具体工具名**，写到 hint 字段）。
- "terminate"：本流量任务已明显进入收尾或反复卡死，建议主 agent 立即调 done()。

判定规则（**只看 window 行为**，不要数 finding 数推 done）：
- **terminate**——仅当 window 满足下列之一时输出：
  a) 反复试同 endpoint 同手法且都失败（≥ 3 步原地打转 / 同 payload 微调 flag 重试）—— 死循环；
  b) window 末尾 agent 自己已明确写出"无漏洞 / 测试完毕 / 该停了"等表态，且主要测试向量都试过；
  c) 已经在 dump 全表 / 枚举所有 ID / 提取额外凭据等明显扩展行为（说明主漏洞早已找到并落库，催 agent 走 update_finding + done 收尾）。
- **redirect**——仅当下列之一时输出：
  a) 当前流量目标是 X（如 sqli），但 window 最近几步在做 Y（如登录折腾 / 测 XSS）—— 方向跑偏；
  b) 最近 1 步含完整工具输出（不截断），里面已出现命中关键字（SUCCESS / vulnerable / uid= / 反射 payload 完整回显 / SQL syntax error）但 window 里**没有** write_finding 调用 —— 必须 hint "立即调用 write_finding 落库当前证据；后续扩展（dump / 链路）走 update_finding 补 evidence"。**禁止**输出"集中验证 / 继续验证 / 再确认"等鼓励延后落库的措辞。
- **continue**——其他一律 continue。包括但不限于：
  a) 本流量任务暂时 0 finding 但 agent 正在合理探索（即便 host 已有别的 finding 也别催 done，每个流量都可能是新漏洞入口）；
  b) 已经落库且在合理扩展（如打 union 取列数）；
  c) window 信息不足以判断方向偏差。

host 已有 finding 段（若存在）仅作背景知识：
- **不要**因为 host 已有 N 条 finding 就推 terminate；那是别的流量的战果，跟本任务无关；
- **不要**因为 host 暂时 0 finding 就推 redirect 改换方向；本流量可能本来就无漏洞，正确判定是让 agent 自行 done。

lesson 段（若存在）也仅作参考——LLM 出现"踩过的坑"行为时（如反复试错误密码），redirect hint 可引用 lesson 解法（如"试 admin:password，见历史经验"）。lesson **不参与 terminate 判定**。

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
		o.readNotesOrNil(ctx),
		o.fetchHostFindingsSection(ctx),
		o.fetchLessonsSection(ctx),
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

// readNotesOrNil 读 engagement 共享笔记板；失败或 store nil 时返回 nil 让 prompt 省略笔记板段。
func (o *LLMReviewer) readNotesOrNil(ctx context.Context) []byte {
	if o.notes == nil {
		return nil
	}
	data, err := o.notes.ReadNotes(ctx, o.engagementID)
	if err != nil {
		slog.Warn("reviewer read state failed", "err", err, "engagement_id", o.engagementID)
		return nil
	}
	return data
}

// fetchHostFindingsSection 调 HostFindingsFetcher 拿该 host 已有 finding 列表，
// 渲染成"背景参考"段（不带 count、不带"已挖数"等暗示进度的措辞）。
// nil hook / 0 条 / 错误 → 返回空串让 buildReviewerPrompt 省略段头。
//
// 设计目的：reviewer 知道 host 漏洞面但**不**把"host 有 N 条"误当成本任务战果。
// 段落标题里明写"不作 terminate 信号"，硬约束 LLM 不把数量当 done 依据。
//
// 渲染示例：
//
//	## 该 host 已有 finding（背景参考，不作 terminate 信号）
//	- [critical] IDOR in /api/bac/order/7
//	- [high] Vertical priv esc in /api/bac/admin/users
func (o *LLMReviewer) fetchHostFindingsSection(ctx context.Context) string {
	if o.HostFindingsFetcher == nil {
		return ""
	}
	items, err := o.HostFindingsFetcher(ctx)
	if err != nil {
		slog.Warn("reviewer fetch host findings failed", "err", err, "engagement_id", o.engagementID)
		return ""
	}
	if len(items) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 该 host 已有 finding（背景参考，**不**作 terminate 信号）\n")
	for _, it := range items {
		b.WriteString("- ")
		b.WriteString(it)
		b.WriteString("\n")
	}
	return b.String()
}

// fetchLessonsSection 调 LessonFetcher 拿该 host 历史 lesson，渲染成 prompt 段；
// nil hook / 0 条 / 错误 → 返回空串。
//
// 渲染示例：
//
//	## 该 host 历史经验（lesson，跨 engagement 累积）
//	- [p7] DVWA 默认密码 admin:password，优先试
//	- [p7] DVWA security=low 需 cookie 强带覆盖 server 强制 impossible
func (o *LLMReviewer) fetchLessonsSection(ctx context.Context) string {
	if o.LessonFetcher == nil {
		return ""
	}
	lessons, err := o.LessonFetcher(ctx)
	if err != nil {
		slog.Warn("reviewer fetch lessons failed", "err", err, "engagement_id", o.engagementID)
		return ""
	}
	if len(lessons) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("## 该 host 历史经验（lesson，跨 engagement 累积）\n")
	for _, l := range lessons {
		b.WriteString("- ")
		b.WriteString(l)
		b.WriteString("\n")
	}
	return b.String()
}

// buildReviewerPrompt 把 flow 摘要 + window + notes + host findings + lessons 段拼成单条 user 消息。
//
//   - flowSummary 为空时省略段头（向后兼容旧调用方）；
//   - window 为空时仍能产出 prompt（空 window 段）；
//   - notes 为 nil 时省略本次扫描笔记板段；
//   - hostFindingsSection 为空时省略 host finding 段（HostFindingsFetcher nil / 出错 / 0 条都可能为空）；
//   - lessonsSection 为空时省略 lesson 段（LessonFetcher nil / 出错 / 0 条都可能为空）；
//   - argsTruncate / obsTruncate 来自 LLMReviewer 的可选字段，控制喂 LLM 的字节数；
//   - 最近 1 步（window 末尾）若 FullObs 非空，用 FullObs 整段（不截断）替代 ObsSummary，
//     让 reviewer 看到 SUCCESS/vulnerable/uid= 等关键字防止摘要丢失误判。
func buildReviewerPrompt(flowSummary string, window []StepRecord, notes []byte, hostFindingsSection, lessonsSection string, argsTruncate, obsTruncate int) string {
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
	if hostFindingsSection != "" {
		b.WriteString("\n")
		b.WriteString(hostFindingsSection)
	}
	if lessonsSection != "" {
		b.WriteString("\n")
		b.WriteString(lessonsSection)
	}
	if len(notes) > 0 {
		b.WriteString("\n本次扫描笔记板（engagement 内同 host 工作笔记）：\n")
		b.Write(notes)
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
