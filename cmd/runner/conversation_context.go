package main

import (
	"context"
	"strings"
	"time"

	"github.com/V3teran/liusha/internal/conversation"
	"github.com/V3teran/liusha/internal/provider"
)

const maxDialogFetch = 200
const dialogHistoryHeader = "## 会话历史（按时间正序）\n"
const dialogDistillInstruction = `你是会话摘要器。把以下早期会话浓缩成 2-4 句中文摘要，保留：用户的关键意图与诉求、已确认的目标/范围、重要结论或决定；省略寒暄与冗余过程。直接输出摘要正文，不要任何前缀。`

func (h handler) conversationContext(ctx context.Context, convID, tier, currentBrief string) string {
	if convID == "" || h.conversations == nil {
		return ""
	}
	msgs, err := h.conversations.ListRecentDialog(ctx, convID, maxDialogFetch)
	if err != nil {
		h.logger.Warn().Err(err).Str("conv", convID).Msg("读会话历史失败（降级：本轮不注入历史）")
		return ""
	}
	kept := filterDialog(msgs, currentBrief)
	if len(kept) == 0 {
		return ""
	}

	older, recent := splitDialogByBudget(kept, h.dialogTokenBudget(ctx, tier))

	var b strings.Builder
	b.WriteString(dialogHistoryHeader)
	if len(older) > 0 {
		if summary := h.distillOldDialog(ctx, older); summary != "" {
			b.WriteString("〔更早会话摘要〕")
			b.WriteString(summary)
			b.WriteString("\n")
		} else {
			writeDialogLines(&b, older)
		}
	}
	writeDialogLines(&b, recent)
	return b.String()
}

func filterDialog(msgs []conversation.Message, currentBrief string) []conversation.Message {
	brief := strings.TrimSpace(currentBrief)
	kept := make([]conversation.Message, 0, len(msgs))
	for _, m := range msgs {
		c := strings.TrimSpace(m.Content)
		if c == "" || c == brief {
			continue
		}
		kept = append(kept, m)
	}
	return kept
}

func splitDialogByBudget(msgs []conversation.Message, budget int) (older, recent []conversation.Message) {
	if budget <= 0 {
		return nil, msgs
	}
	acc := 0
	cut := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		acc += estimateDialogTokens(msgs[i].Content)
		if acc > budget {
			cut = i + 1
			break
		}
	}
	return msgs[:cut], msgs[cut:]
}

func estimateDialogTokens(content string) int {
	return (len(strings.TrimSpace(content)) + 3) / 4
}

func writeDialogLines(b *strings.Builder, msgs []conversation.Message) {
	for _, m := range msgs {
		b.WriteString(dialogSpeaker(m.Role))
		b.WriteString("：")
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString("\n")
	}
}

func formatDialogHistory(msgs []conversation.Message, currentBrief string) string {
	kept := filterDialog(msgs, currentBrief)
	if len(kept) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(dialogHistoryHeader)
	writeDialogLines(&b, kept)
	return b.String()
}

// dialogTokenBudget 算 dialog 段 token 预算 = TrailingBudgetRatio × ContextWindow。
// ContextWindow 通过 provider.CountTokens 不可得（那是请求级）；从 router 元数据获取。
// 暂无 ContextWindow API 时降级返回 0（全 verbatim）。
func (h handler) dialogTokenBudget(ctx context.Context, tier string) int {
	compaction, _ := h.settings.Compaction(ctx)
	ratio := compaction.TrailingBudgetRatio
	if ratio <= 0 {
		ratio = 0.50
	}
	// 若能从 router 取到 context window 则精确计算，否则降级为不蒸馏
	cw := h.contextWindowFor(tier)
	if cw <= 0 {
		return 0
	}
	return int(float64(cw) * ratio)
}

// contextWindowFor 按 tier 返回已知模型的上下文窗口（token）。
// 无匹配时返回 0（caller 降级为全 verbatim，不蒸馏）。
func (h handler) contextWindowFor(tier string) int {
	switch tier {
	case "planner":
		return 200_000
	case "executor":
		return 200_000
	case "inspector":
		return 200_000
	}
	return 0
}

func (h handler) distillOldDialog(ctx context.Context, msgs []conversation.Message) string {
	p, err := h.router.For(ctx, provider.ComplexitySimple)
	if err != nil {
		h.logger.Warn().Err(err).Msg("② 旧会话蒸馏：解析 inspector provider 失败（降级：直接拼接）")
		return ""
	}

	msgs2 := make([]provider.Message, 0, len(msgs)+1)
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if m.Role == conversation.RoleAssistant {
			msgs2 = append(msgs2, provider.Message{Role: provider.RoleAssistant, Content: content})
		} else {
			msgs2 = append(msgs2, provider.Message{Role: provider.RoleUser, Content: dialogSpeaker(m.Role) + "：" + content})
		}
	}
	msgs2 = append(msgs2, provider.Message{Role: provider.RoleUser, Content: dialogDistillInstruction})

	compaction, _ := h.settings.Compaction(ctx)
	timeout := compaction.CompactorTimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	dctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	resp, err := p.Complete(dctx, provider.Request{
		Messages:  msgs2,
		MaxTokens: 512,
	})
	if err != nil {
		h.logger.Warn().Err(err).Msg("② 旧会话蒸馏：provider 调用失败（降级：直接拼接）")
		return ""
	}
	return strings.TrimSpace(resp.Content)
}

func dialogSpeaker(r conversation.Role) string {
	switch r {
	case conversation.RoleUser:
		return "用户"
	case conversation.RoleAssistant:
		return "助手"
	case conversation.RoleSystem:
		return "系统"
	default:
		return string(r)
	}
}
