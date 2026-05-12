package react

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"github.com/V3teran/liusha/internal/llm"
)

// StateReader 是 LLMObserver 读取 engagement 三层 memory 的最小依赖。
// *engagement.Store 隐式满足该接口，测试可注入 stub。
type StateReader interface {
	ReadState(ctx context.Context, id string) ([]byte, error)
}

// LLMObserver 用 light_provider LLM 在 ReAct 循环每 N 步做一次进度评估。
//
// 设计要点：
//   - 不阻塞主循环：LLM 错 / JSON 解析失败 / 非法 decision 一律回退 continue；
//   - 紧凑 prompt：当前流量摘要 + 最近窗口（含 ObsSummary）+ 状态板，≤ 几百 token；
//   - 严格 JSON 输出契约：`{"decision":"...","hint":"..."}`，三种合法值。
// fallback 截断阈值：caller 未注入对应字段时使用。
const (
	fallbackObserverArgsTruncate = 80
	fallbackObserverObsTruncate  = 120
)

type LLMObserver struct {
	llm          llm.Generator
	state        StateReader
	engagementID string

	// 可选字段：caller 通常从 cfg.React.{ObserverArgsTruncate, ObserverObsTruncate} 注入。
	// 零值走 fallback 常量。
	ArgsTruncate int
	ObsTruncate  int

	// FlowSummary 是当前流量任务的一句话摘要（如 "GET vulnapp.local/api/user?id=1"），
	// 用于约束 observer 只评本流量进度，不要把 agent 推向其它流量任务。
	// 零值时 prompt 省略该段——退化为旧"无 flow 上下文"行为。
	FlowSummary string
}

func (o *LLMObserver) effectiveArgsTruncate() int {
	if o.ArgsTruncate > 0 {
		return o.ArgsTruncate
	}
	return fallbackObserverArgsTruncate
}

func (o *LLMObserver) effectiveObsTruncate() int {
	if o.ObsTruncate > 0 {
		return o.ObsTruncate
	}
	return fallbackObserverObsTruncate
}

// NewLLMObserver 用 router.For("observer") 路由出的 light Generator + engagement store 构造。
//
// store 可为 nil（测试场景），此时 prompt 中省略 memory 状态板。
func NewLLMObserver(g llm.Generator, store StateReader, engagementID string) *LLMObserver {
	return &LLMObserver{llm: g, state: store, engagementID: engagementID}
}

// observerSystemPrompt 约束 observer 只评本流量进度、输出严格 JSON。
const observerSystemPrompt = `你是漏洞挖掘主 agent 的进度评估者。基于"当前流量任务摘要 + 最近 N 步动作 + 状态板"评估进度，只评本流量任务，不要把 agent 推向其它流量或别的 host。

只能从以下三种 decision 中选一个：
- "continue"：进度正常或主漏洞已落库进入收尾，继续当前路径。
- "redirect"：当前路径有偏差，给一句 ≤ 100 字的中文方向提示（漏洞类型 / 验证阶段 / 验证手法层级，**禁止点具体工具名**，写到 hint 字段）。
- "terminate"：当前流量任务已挖完或反复卡死无进展，建议主 agent 立即调 done()。

严格只输出一个 JSON 对象，禁止任何多余文本：
{"decision":"continue|redirect|terminate","hint":"..."}`

// observerDecision 是 LLM 必须返回的 JSON 结构。
type observerDecision struct {
	Decision string `json:"decision"`
	Hint     string `json:"hint"`
}

// Evaluate 实现 Observer。
//
// 任何失败路径（store 读失败 / LLM 调用失败 / JSON 解析失败 / decision 非法）
// 都返回 keep_going，避免阻塞主循环——失败本身已写 warn 日志。
func (o *LLMObserver) Evaluate(ctx context.Context, window []StepRecord) Verdict {
	user := buildObserverPrompt(o.FlowSummary, window, o.readStateOrNil(ctx), o.effectiveArgsTruncate(), o.effectiveObsTruncate())

	res, err := o.llm.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: observerSystemPrompt},
		{Role: llm.RoleUser, Content: user},
	}, nil)
	if err != nil {
		slog.Warn("observer llm call failed", "err", err, "engagement_id", o.engagementID)
		return Verdict{Decision: VerdictContinue}
	}

	var dec observerDecision
	content := strings.TrimSpace(res.Content)
	if err := json.Unmarshal([]byte(content), &dec); err != nil {
		slog.Warn("observer parse json failed", "raw", content, "engagement_id", o.engagementID)
		return Verdict{Decision: VerdictContinue}
	}

	if v := normalizeDecision(dec.Decision); v != "" {
		return Verdict{Decision: v, Hint: dec.Hint}
	}
	slog.Warn("observer unknown decision", "decision", dec.Decision, "engagement_id", o.engagementID)
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
func (o *LLMObserver) readStateOrNil(ctx context.Context) []byte {
	if o.state == nil {
		return nil
	}
	data, err := o.state.ReadState(ctx, o.engagementID)
	if err != nil {
		slog.Warn("observer read state failed", "err", err, "engagement_id", o.engagementID)
		return nil
	}
	return data
}

// buildObserverPrompt 把 flow 摘要 + window + state 拼成单条 user 消息。
//
//   - flowSummary 为空时省略段头（向后兼容旧调用方）；
//   - window 为空时仍能产出 prompt（空 window 段）；
//   - state 为 nil 时省略状态板段。
//   - argsTruncate / obsTruncate 来自 LLMObserver 的可选字段，控制喂 LLM 的字节数。
func buildObserverPrompt(flowSummary string, window []StepRecord, state []byte, argsTruncate, obsTruncate int) string {
	var b strings.Builder
	if flowSummary != "" {
		fmt.Fprintf(&b, "当前流量任务：%s\n\n", flowSummary)
	}
	fmt.Fprintf(&b, "最近 %d 步动作：\n", len(window))
	for i, w := range window {
		fmt.Fprintf(&b, "%d. %s(%s) → %s\n", i+1, w.ActionName, truncate(string(w.Args), argsTruncate), truncate(w.ObsSummary, obsTruncate))
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
