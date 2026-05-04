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

// LLMObserver 用 light_provider LLM 在 ReAct 循环每 N 步做一次"判官"决策。
//
// 设计要点（黑客松借鉴共识 A）：
//   - 不阻塞主循环：LLM 错 / JSON 解析失败 / 非法 decision 一律回退 keep_going；
//   - 紧凑 prompt：只把最近窗口（含 ObsSummary）+ 三层 memory 拼成 ≤ 几百 token；
//   - 严格 JSON 输出契约：`{"decision":"...","hint":"..."}`，三种合法值。
type LLMObserver struct {
	llm          llm.Generator
	state        StateReader
	engagementID string
}

// NewLLMObserver 用 router.For("observer") 路由出的 light Generator + engagement store 构造。
//
// store 可为 nil（测试场景），此时 prompt 中省略 memory 状态板。
func NewLLMObserver(g llm.Generator, store StateReader, engagementID string) *LLMObserver {
	return &LLMObserver{llm: g, state: store, engagementID: engagementID}
}

// observerSystemPrompt 是固定的判官 system 提示。约束输出严格 JSON。
const observerSystemPrompt = `你是 ReAct 循环的"过程判官"。基于最近 N 步动作和当前状态板，判断主循环该如何继续。

只能从以下三种 decision 中选一个：
- "keep_going"：方向正确，继续当前路径。
- "steer_with_hint"：方向偏了，给一句 ≤ 100 字的中文提示让它改向（写到 hint 字段）。
- "abort_low_value"：明显在原地打转或完全偏题，建议直接终止。

严格只输出一个 JSON 对象，禁止任何多余文本：
{"decision":"keep_going|steer_with_hint|abort_low_value","hint":"..."}`

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
	user := buildObserverPrompt(window, o.readStateOrNil(ctx))

	res, err := o.llm.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: observerSystemPrompt},
		{Role: llm.RoleUser, Content: user},
	}, nil)
	if err != nil {
		slog.Warn("observer llm call failed", "err", err, "engagement_id", o.engagementID)
		return Verdict{Decision: VerdictKeepGoing}
	}

	var dec observerDecision
	content := strings.TrimSpace(res.Content)
	if err := json.Unmarshal([]byte(content), &dec); err != nil {
		slog.Warn("observer parse json failed", "raw", content, "engagement_id", o.engagementID)
		return Verdict{Decision: VerdictKeepGoing}
	}

	if v := normalizeDecision(dec.Decision); v != "" {
		return Verdict{Decision: v, Hint: dec.Hint}
	}
	slog.Warn("observer unknown decision", "decision", dec.Decision, "engagement_id", o.engagementID)
	return Verdict{Decision: VerdictKeepGoing}
}

// normalizeDecision 把 LLM 输出的 decision 字段归一化到 3 种合法值。
//
// 设计原因：LLM 偶发 typo（如 "kepp_going" 漏字母）/ 大小写差异 / 含连字符变体
// 会被旧 strict switch 直接拒绝（VerdictKeepGoing 兜底但打 warn）。这里用宽松匹配吸收
// 常见变体，避免噪音日志：
//   - 包含 "abort" → abort_low_value
//   - 包含 "steer" → steer_with_hint
//   - 包含 "keep" / "going" / "kepp" → keep_going（覆盖 LLM 拼写错误）
//
// 完全无法识别返回 ""，让 caller 仍 fallback keep_going + warn（保留可观测性）。
func normalizeDecision(raw string) string {
	s := strings.ToLower(strings.TrimSpace(raw))
	switch {
	case strings.Contains(s, "abort"):
		return VerdictAbort
	case strings.Contains(s, "steer"):
		return VerdictSteer
	case strings.Contains(s, "keep"), strings.Contains(s, "going"), strings.Contains(s, "kepp"):
		return VerdictKeepGoing
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

// buildObserverPrompt 把 window + state 拼成单条 user 消息。
//
//   - window 为空时仍能产出 prompt（空 window 段）；
//   - state 为 nil 时省略状态板段。
func buildObserverPrompt(window []StepRecord, state []byte) string {
	var b strings.Builder
	fmt.Fprintf(&b, "最近 %d 步动作：\n", len(window))
	for i, w := range window {
		fmt.Fprintf(&b, "%d. %s(%s) → %s\n", i+1, w.ActionName, truncate(string(w.Args), 80), truncate(w.ObsSummary, 120))
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
