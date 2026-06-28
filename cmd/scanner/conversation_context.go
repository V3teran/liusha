package main

import (
	"context"
	"strings"
	"time"

	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/adk/middlewares/summarization"
	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/conversation"
)

// conversation_context.go：② 对话历史工作记忆（跨 run 的多轮连贯）。
//
// eino summarization（①）管的是单 run 内存上下文，管不到「跨 run 的对话历史拼进 prompt」这层。
// ② 自己实现「token 预算 + 最近完整 + 旧的蒸馏」（与 ① 不同机制，分层正确，见 docs/memory-architecture-refactor）：
//   - 从 message 表拉对话（最多 maxDialogFetch 条）
//   - token 预算窗口（TrailingBudgetRatio × provider.ContextWindow）：预算内的最近对话原样保留
//   - 超预算的更早对话用 light 模型蒸馏成 1 段摘要（旧的不丢、但不膨胀 prompt）
//   - 每轮重算（方案 A，不缓存）：短对话零额外开销，仅长对话每轮 +1 次 light 蒸馏

// maxDialogFetch 是从 message 表拉取对话历史的条数硬上界（store clampLimit 会再夹）；
// 真正的裁剪闸门是下面的 token 预算窗口，此值仅防超长对话全量拉爆内存。
const maxDialogFetch = 200

// dialogHistoryHeader 是「对话历史」prompt 段标题。
const dialogHistoryHeader = "## 对话历史（按时间正序）\n"

// dialogDistillInstruction 是旧对话蒸馏的 system 指引（light 模型，对话维度，区别于 ① 的 ReAct 蒸馏）。
const dialogDistillInstruction = `你是对话摘要器。把以下早期对话浓缩成 2-4 句中文摘要，保留：用户的关键意图与诉求、已确认的目标/范围、重要结论或决定；省略寒暄与冗余过程。直接输出摘要正文，不要任何前缀。`

// conversationContext 读该对话历史，按 token 预算保最近完整 + 旧的蒸馏，拼成「对话历史」prompt 段。
//
// role 决定 provider 上下文窗口（dialog 预算 = TrailingBudgetRatio × ContextWindow）。
// convID 空 / store nil / 无历史 / 读失败 → 返空串（首轮或降级，不阻塞扫描）。
// 排除 currentBrief（本轮用户消息，已由 BuildUserPrompt 注入，避免重复）。
func (h handler) conversationContext(ctx context.Context, convID, role, currentBrief string) string {
	if convID == "" || h.conversations == nil {
		return ""
	}
	msgs, err := h.conversations.ListRecentDialog(ctx, convID, maxDialogFetch)
	if err != nil {
		h.logger.Warn().Err(err).Str("conv", convID).Msg("读对话历史失败（降级：本轮不注入历史）")
		return ""
	}
	kept := filterDialog(msgs, currentBrief)
	if len(kept) == 0 {
		return ""
	}

	older, recent := splitDialogByBudget(kept, h.dialogTokenBudget(role))

	var b strings.Builder
	b.WriteString(dialogHistoryHeader)
	if len(older) > 0 {
		if summary := h.distillOldDialog(ctx, older); summary != "" {
			b.WriteString("〔更早对话摘要〕")
			b.WriteString(summary)
			b.WriteString("\n")
		} else {
			// 蒸馏失败：旧对话直接拼接（不丢），交由 ① 在 run 内兜底压缩。
			writeDialogLines(&b, older)
		}
	}
	writeDialogLines(&b, recent)
	return b.String()
}

// filterDialog 过滤空内容 + 本轮 brief（已在 BuildUserPrompt，避免重复），保留其余（纯逻辑，可测）。
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

// splitDialogByBudget 从尾部（最近）反向累加 token，预算内的归 recent，更早的归 older（均保持时间正序）。
// budget <= 0（provider 未配 ContextWindow）时全部归 recent（不蒸馏，降级）。整条消息为粒度，不切断单条。
func splitDialogByBudget(msgs []conversation.Message, budget int) (older, recent []conversation.Message) {
	if budget <= 0 {
		return nil, msgs
	}
	acc := 0
	cut := 0 // recent 起点 index：msgs[cut:]=recent，msgs[:cut]=older
	for i := len(msgs) - 1; i >= 0; i-- {
		acc += estimateDialogTokens(msgs[i].Content)
		if acc > budget {
			cut = i + 1
			break
		}
	}
	return msgs[:cut], msgs[cut:]
}

// estimateDialogTokens 按 chars/4 粗估单条对话 token（与 eino 默认 TokenCounter 同口径）。
func estimateDialogTokens(content string) int {
	return (len(strings.TrimSpace(content)) + 3) / 4
}

// writeDialogLines 把对话消息逐条写成「角色：内容」行（纯逻辑，无标题无过滤）。
func writeDialogLines(b *strings.Builder, msgs []conversation.Message) {
	for _, m := range msgs {
		b.WriteString(dialogSpeaker(m.Role))
		b.WriteString("：")
		b.WriteString(strings.TrimSpace(m.Content))
		b.WriteString("\n")
	}
}

// formatDialogHistory 过滤空+本轮 brief 后，格式化成带标题的「对话历史」段；全空返空串（纯逻辑，可测）。
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

// dialogTokenBudget 算 dialog 段 token 预算 = TrailingBudgetRatio × provider.ContextWindow。
// provider 未配 ContextWindow（返 0）时返 0 → splitDialogByBudget 降级为全 verbatim。
func (h handler) dialogTokenBudget(role string) int {
	cw := h.einoFactory.ContextWindowFor(role)
	if cw <= 0 {
		return 0
	}
	ratio := h.cfg.React.HistoryCompact.TrailingBudgetRatio
	if ratio <= 0 {
		ratio = 0.50
	}
	return int(float64(cw) * ratio)
}

// distillOldDialog 把更早的对话蒸馏成 1 段摘要；失败返空串（caller 降级为直接拼接，不丢历史）。
//
// 复用 eino 的同步蒸馏 summarization.SummarizeMessages（拥抱 eino：模型输入构造/重试由它管）；
// ② 自己只做 token 预算切分（eino 无跨 run 对话概念，那部分无法复用）。关 PreserveUserMessages
// （最近/旧切分 ② 自己已做）；取 ModelResponse（原始摘要，不带 eino 的 compaction 前导语/续接指令）。
func (h handler) distillOldDialog(ctx context.Context, msgs []conversation.Message) string {
	compactor, err := h.einoFactory.For(ctx, "compactor")
	if err != nil {
		h.logger.Warn().Err(err).Msg("② 旧对话蒸馏：解析 compactor 模型失败（降级：旧对话直接拼接）")
		return ""
	}

	// 转 eino 消息，保留 user/assistant 角色让摘要器看清对话结构。
	ems := make([]adk.Message, 0, len(msgs))
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if m.Role == conversation.RoleAssistant {
			ems = append(ems, schema.AssistantMessage(content, nil))
		} else {
			ems = append(ems, schema.UserMessage(dialogSpeaker(m.Role)+"："+content))
		}
	}

	timeout := h.cfg.React.HistoryCompact.CompactorTimeoutSeconds
	if timeout <= 0 {
		timeout = 30
	}
	dctx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	out, err := summarization.SummarizeMessages(dctx, &summarization.Config{
		Model:                compactor,
		UserInstruction:      dialogDistillInstruction,
		PreserveUserMessages: &summarization.PreserveUserMessages{Enabled: false},
	}, ems)
	if err != nil || out == nil || out.ModelResponse == nil {
		h.logger.Warn().Err(err).Msg("② 旧对话蒸馏：eino SummarizeMessages 失败（降级：旧对话直接拼接）")
		return ""
	}
	return strings.TrimSpace(out.ModelResponse.Content)
}

// dialogSpeaker 把消息角色转成中文发言人标签。
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
