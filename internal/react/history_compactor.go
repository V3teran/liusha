// Package react ReAct msgs 滑窗压缩。
//
// 目标：每步 Generate 前算 total tokens，超 trigger_ratio × ctx_window 触发蒸馏，
// 永保 system + 首 user，trailing 反向累加保最近 budget；候选集一次性送 light LLM 蒸馏成 1 条 user msg；
// 失败 head-truncate 兜底，不阻断 ReAct 主循环。
//
// 设计哲学：与 notes/lesson/finding 分层记忆协同——蒸馏 prompt 引导省略已 write_* 上提的内容，
// 下游 LLM 可调 read_findings / read_lessons / read_notes 工具按需重读。
package react

import (
	"context"
	_ "embed"
	"fmt"
	"strings"

	"github.com/V3teran/liusha/internal/llm"
)

// historyCompactorSystemPrompt 是 LLMHistoryCompactor 用的 system prompt，编译期 embed。
//
//go:embed history_compactor_prompt.md
var historyCompactorSystemPrompt string

// summaryWrapperTag 是蒸馏后摘要消息包裹标签，下游 LLM 见此标签知"这是历史压缩"。
// 用中文领域化标签更对齐国内 LLM 训练分布。
const summaryWrapperTag = "历史片段摘要"

// dedupTools 是 read_* 系列查询工具的名字集——其 tool_result 是可重现查询结果，
// 同名多次调用旧的可丢（hunter 想看 read 一次即可），节省 token 显著（finding 列表常含数 KB JSON）。
// 写类工具（write_finding 等）不去重——每次写入语义不同。
var dedupTools = map[string]struct{}{
	"read_findings":  {},
	"read_lessons":   {},
	"read_notes":     {},
	"read_relations": {},
}

// dedupPlaceholderTpl 是被去重折叠后的 tool message content，标识"此处曾有 N 次重复同名读"。
const dedupPlaceholderTpl = "[此处 %d 次重复 %s 调用已折叠 — 如需最新结果请直接调 %s]"

// HistoryCompactor 是 ReAct msgs 蒸馏接口。
//
// Compact 把 oldTurns 蒸馏成 1 条 user-role 消息（标签 <历史片段摘要>）。
// ctx 含 deadline；超时/失败返回 err，caller 退化为 head-truncate 兜底。
type HistoryCompactor interface {
	Compact(ctx context.Context, oldTurns []llm.Message) (llm.Message, error)
}

// NoopHistoryCompactor 是测试用空实现——总是返回 error，强制 caller 走 head-truncate 路径。
// 单测验证 fallback 行为时注入。
type NoopHistoryCompactor struct{}

// Compact 永远返 error。
func (NoopHistoryCompactor) Compact(_ context.Context, _ []llm.Message) (llm.Message, error) {
	return llm.Message{}, fmt.Errorf("noop compactor")
}

// LLMHistoryCompactor 用 light LLM 蒸馏老 ReAct turn。
//
// gen 必填——通常注入 cfg.Providers[light_provider] 路由出的 Generator。
type LLMHistoryCompactor struct {
	gen llm.Generator
}

// NewLLMHistoryCompactor 构造 LLMHistoryCompactor；gen 必填。
func NewLLMHistoryCompactor(gen llm.Generator) *LLMHistoryCompactor {
	return &LLMHistoryCompactor{gen: gen}
}

// Compact 把 oldTurns 序列化成 role: content 顺序文本喂给 LLM，封装成 1 条 user msg 返回。
// LLM 返回空文本 / 超时 / generate 失败 → 返 err。
func (c *LLMHistoryCompactor) Compact(ctx context.Context, oldTurns []llm.Message) (llm.Message, error) {
	if len(oldTurns) == 0 {
		return llm.Message{}, fmt.Errorf("Compact: oldTurns 为空")
	}

	var b strings.Builder
	for _, m := range oldTurns {
		fmt.Fprintf(&b, "%s: %s\n", m.Role, extractMessageText(m))
	}

	res, err := c.gen.Generate(ctx, []llm.Message{
		{Role: llm.RoleSystem, Content: historyCompactorSystemPrompt},
		{Role: llm.RoleUser, Content: b.String()},
	}, nil)
	if err != nil {
		return llm.Message{}, fmt.Errorf("Compact: LLM Generate 失败: %w", err)
	}

	summary := strings.TrimSpace(res.Content)
	if summary == "" {
		return llm.Message{}, fmt.Errorf("Compact: LLM 返回空摘要")
	}

	return llm.Message{
		Role: llm.RoleUser,
		Content: fmt.Sprintf("<%s turns=\"%d\">\n%s\n</%s>",
			summaryWrapperTag, len(oldTurns), summary, summaryWrapperTag),
	}, nil
}

// extractMessageText 从 llm.Message 抽取纯文本表示（喂蒸馏器用）。
// content / contentParts / tool_calls 都做最佳努力打印；image 替换占位。
func extractMessageText(m llm.Message) string {
	if m.Content != "" {
		return m.Content
	}
	if len(m.ContentParts) > 0 {
		pieces := make([]string, 0, len(m.ContentParts))
		for _, p := range m.ContentParts {
			switch p.Type {
			case "text":
				if p.Text != "" {
					pieces = append(pieces, p.Text)
				}
			case "image_url":
				pieces = append(pieces, "[图片]")
			}
		}
		return strings.Join(pieces, " ")
	}
	if len(m.ToolCalls) > 0 {
		out := make([]string, 0, len(m.ToolCalls))
		for _, tc := range m.ToolCalls {
			out = append(out, fmt.Sprintf("tool_call(%s, %s)", tc.Name, string(tc.Arguments)))
		}
		return strings.Join(out, "; ")
	}
	return ""
}

// estimateMessageTokens 按 chars/4 粗估单条消息 token 数（含 content/contentParts/tool_calls）。
// image_url base64 也按字符数算（粗估而非精算，跨 provider 通用）。
func estimateMessageTokens(m llm.Message) int {
	total := len(m.Content) / 4
	for _, p := range m.ContentParts {
		switch p.Type {
		case "text":
			total += len(p.Text) / 4
		case "image_url":
			if p.ImageURL != nil {
				// base64 实际占 image token 由 provider 编码器决定，但 chars/4 是合理上界粗估
				total += len(p.ImageURL.Base64Data) / 4
			}
		}
	}
	for _, tc := range m.ToolCalls {
		total += (len(tc.Name) + len(tc.Arguments)) / 4
	}
	return total
}

// estimateTotalTokens 求 msgs 总 tokens（含所有消息字段）。
func estimateTotalTokens(msgs []llm.Message) int {
	total := 0
	for _, m := range msgs {
		total += estimateMessageTokens(m)
	}
	return total
}

// dedupReadToolResults 倒序遍历 msgs，对 dedupTools 集合内同名 tool result 仅保最新 1 次；
// 老的 tool message content 原地替换成 dedupPlaceholderTpl 占位。
// in-place 修改不分配新 slice。
//
// 副作用：仅修改 .Content/.ContentParts 字段；.Role/.Name/.ToolCallID 保留，
// 与 assistant.tool_calls 配对完整性不破坏。
func dedupReadToolResults(msgs []llm.Message) {
	seen := make(map[string]int) // tool name → 已保留次数（≥1 即跳过后续）
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != llm.RoleTool || m.Name == "" {
			continue
		}
		if _, dedup := dedupTools[m.Name]; !dedup {
			continue
		}
		count := seen[m.Name]
		if count == 0 {
			// 最新一次保留
			seen[m.Name] = 1
			continue
		}
		// 老的同名 read：折叠
		msgs[i].Content = fmt.Sprintf(dedupPlaceholderTpl, count+1, m.Name, m.Name)
		msgs[i].ContentParts = nil
		seen[m.Name] = count + 1
	}
}

// findFirstUserMsgIndex 按 index 取首条 user msg——按 role 找会撞 Inspector hint（也是 user role）。
// 返回 -1 表示未找到（不应发生，runtime.Run 一定在 msgs[1] 注入）。
func findFirstUserMsgIndex(msgs []llm.Message) int {
	// runtime 约定 msgs[0]=system，msgs[1]=首 user prompt
	if len(msgs) >= 2 && msgs[1].Role == llm.RoleUser {
		return 1
	}
	// 退化场景（无 system / 无 user）：按 role 找首条
	for i, m := range msgs {
		if m.Role == llm.RoleUser {
			return i
		}
	}
	return -1
}

// turnBoundary 判断 msg 是否是 ReAct turn 起点（assistant + tool_calls）。
// 用于 trailing window 反向累加时不切断 assistant.tool_calls 与 tool_result 配对。
func turnBoundary(m llm.Message) bool {
	return m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0
}

// trailingByTokenBudget 反向按"完整 ReAct turn"单位累加保最近消息；budget 用尽时**整个 turn**丢弃。
//
// 算法：每轮先反向定位 turn 起点（assistant.tool_calls），算 turn 总 tokens；
// 如果加入后超 budget → 此 turn 不要，返回当前 keepFromIdx（已指向上一个完整 turn 起点）；
// 否则把此 turn 纳入 trailing，keepFromIdx = turn 起点，继续上一轮。
//
// 返回的索引 keepFromIdx 表示：msgs[keepFromIdx:] 是 trailing window，**严格按 turn 边界对齐**。
// 若无任何完整 turn 能容纳 → 返回 len(msgs)（trailing 为空，所有候选都送压缩）。
func trailingByTokenBudget(msgs []llm.Message, firstUserIdx int, budget int) int {
	accumulated := 0
	keepFromIdx := len(msgs)
	i := len(msgs) - 1
	for i > firstUserIdx {
		// 反向定位 turn 起点：连续往前找直到遇到 turnBoundary（assistant + tool_calls）。
		// 中间的 tool / user(inspector hint) 都算 turn 的一部分。
		turnStart := i
		for turnStart > firstUserIdx && !turnBoundary(msgs[turnStart]) {
			turnStart--
		}
		if turnStart <= firstUserIdx {
			// 找不到完整 turn 起点（msgs 没有 assistant.tool_calls，可能是初始 user prompt 之后未生成）
			break
		}

		turnTokens := 0
		for j := turnStart; j <= i; j++ {
			turnTokens += estimateMessageTokens(msgs[j])
		}

		if accumulated+turnTokens > budget {
			// 此 turn 加入后超 budget → 整 turn 丢弃，返回当前 keepFromIdx
			return keepFromIdx
		}
		accumulated += turnTokens
		keepFromIdx = turnStart
		i = turnStart - 1
	}
	return keepFromIdx
}

// headTruncateByBudget 是兜底降级策略：LLM 蒸馏失败时调用。
// 保 msgs[0] (system) + msgs[firstUserIdx] (首 user) + trailing window（反向累加直到 budget 用尽）。
// 返回新 slice（不修改原 msgs）。
func headTruncateByBudget(msgs []llm.Message, budget int) []llm.Message {
	firstUserIdx := findFirstUserMsgIndex(msgs)
	if firstUserIdx < 0 {
		return msgs // 防御：无 user msg，原样返回
	}
	keepFromIdx := trailingByTokenBudget(msgs, firstUserIdx, budget)
	if keepFromIdx <= firstUserIdx+1 {
		return msgs // trailing 已覆盖全部，无需截断
	}
	// 拼装：[system] + [首 user] + msgs[keepFromIdx:]
	out := make([]llm.Message, 0, 2+(len(msgs)-keepFromIdx))
	if len(msgs) > 0 && msgs[0].Role == llm.RoleSystem {
		out = append(out, msgs[0])
	}
	out = append(out, msgs[firstUserIdx])
	out = append(out, msgs[keepFromIdx:]...)
	return out
}

// compactState 是 compactHistory 跨 step 调用的状态（cooldown 节流用）。
// runtime.Run 内栈上 alloc 即可，不需 thread-safe。
type compactState struct {
	lastCompactedTokens int // 上次压缩后的 total tokens，cooldown 判定基线
}

// HistoryCompactConfig 是 runtime 独立的压缩超参——与 config.HistoryCompactConfig 字段对称。
// runtime 不依赖 config 包；caller（cmd/scanner 装配层）把 config 字段一一映射进来。
type HistoryCompactConfig struct {
	TriggerRatio        float64 // > 此比例 × ContextWindow 触发；默认 0.75
	TrailingBudgetRatio float64 // trailing window 占 ContextWindow 比例；默认 0.50
	CooldownTokenDelta  int     // 距上次压缩净增 < 此值跳过；默认 4000
}

// compactHistory 是 runtime 每步调用的总入口。
//
// 流程：
//  1. dedupReadToolResults（O(n)，便宜）
//  2. estimateTotalTokens
//  3. 不超 trigger ratio → 返原 msgs
//  4. 距上次压缩净增 < cooldown_token_delta → 返原 msgs
//  5. 划分 [永保 system+首 user] / [trailing window] / [候选集]
//  6. 候选集送 LLM 蒸馏 → 拼回；失败 → head-truncate 兜底
//
// 出参更新 state.lastCompactedTokens；caller 持有 state 指针。
//
// 入参字段说明：
//   - msgs           : 当前 hunter 全部历史
//   - compactor      : HistoryCompactor（nil → 直接跳过压缩，等价 disabled）
//   - ctxWindow      : provider.context_window，必须 > 0
//   - cfg            : 压缩超参（trigger_ratio / trailing_budget_ratio / cooldown_token_delta）
//   - state          : 跨 step cooldown 状态（caller 持有）
//
// 返回压缩后的 msgs（可能等于入参）。
func compactHistory(
	ctx context.Context,
	msgs []llm.Message,
	compactor HistoryCompactor,
	ctxWindow int,
	cfg HistoryCompactConfig,
	state *compactState,
) []llm.Message {
	if compactor == nil || ctxWindow <= 0 {
		return msgs
	}

	// 1. 零成本去重
	dedupReadToolResults(msgs)

	// 2. token 计数
	total := estimateTotalTokens(msgs)
	threshold := int(float64(ctxWindow) * cfg.TriggerRatio)
	if total <= threshold {
		return msgs
	}

	// 3. cooldown 节流
	if state.lastCompactedTokens > 0 && total-state.lastCompactedTokens < cfg.CooldownTokenDelta {
		return msgs
	}

	// 4. 划分 trailing window
	firstUserIdx := findFirstUserMsgIndex(msgs)
	if firstUserIdx < 0 {
		return msgs
	}
	trailingBudget := int(float64(ctxWindow) * cfg.TrailingBudgetRatio)
	keepFromIdx := trailingByTokenBudget(msgs, firstUserIdx, trailingBudget)
	candidateEnd := keepFromIdx
	candidateStart := firstUserIdx + 1
	if candidateEnd <= candidateStart {
		// 候选集为空（trailing 已覆盖到首 user 之后），无可压缩
		return msgs
	}
	candidates := msgs[candidateStart:candidateEnd]

	// 5. LLM 蒸馏
	summaryMsg, err := compactor.Compact(ctx, candidates)
	if err != nil {
		// 6. 失败兜底 head-truncate
		out := headTruncateByBudget(msgs, int(float64(ctxWindow)*cfg.TriggerRatio))
		state.lastCompactedTokens = estimateTotalTokens(out)
		return out
	}

	// 7. 拼回：[system?] + [首 user] + [summaryMsg] + [trailing]
	out := make([]llm.Message, 0, candidateStart+1+(len(msgs)-keepFromIdx))
	out = append(out, msgs[:candidateStart]...) // system + 首 user
	out = append(out, summaryMsg)
	out = append(out, msgs[keepFromIdx:]...)

	state.lastCompactedTokens = estimateTotalTokens(out)
	return out
}

// 编译期校验 LLMHistoryCompactor / NoopHistoryCompactor 实现 HistoryCompactor 接口。
var (
	_ HistoryCompactor = (*LLMHistoryCompactor)(nil)
	_ HistoryCompactor = NoopHistoryCompactor{}
)
