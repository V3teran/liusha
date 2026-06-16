package main

import (
	"context"
	"strconv"
	"strings"

	"github.com/V3teran/liusha/internal/conversation"
)

// maxDialogHistory 是阶段0 喂给 agent 的对话历史条数上限（仅普通对话消息，不含过程事件）。
// 阶段0 先固定条数；阶段1 改为按 token 预算 + 旧的压缩（复用 eino compaction）。
const maxDialogHistory = 30

// conversationContext 读该对话最近的「普通对话消息」，格式化成「对话历史」prompt 段，
// 供 active orchestrator 看到多轮上下文（解决追问断片）。
//
// 排除 currentBrief（本轮用户消息，已由 BuildUserPrompt 注入，避免重复）。
// convID 空 / store nil / 无历史 / 读失败 → 返空串（首轮或降级，不阻塞扫描）。
func (h handler) conversationContext(ctx context.Context, convID, currentBrief string) string {
	if convID == "" || h.conversations == nil {
		return ""
	}
	msgs, err := h.conversations.ListRecentDialog(ctx, convID, maxDialogHistory)
	if err != nil {
		h.logger.Warn().Err(err).Str("conv", convID).Msg("读对话历史失败（降级：本轮不注入历史）")
		return ""
	}
	return formatDialogHistory(msgs, currentBrief)
}

// formatDialogHistory 把对话消息格式化成「对话历史」prompt 段（纯逻辑，可测）。
// 跳过空内容 + 本轮 brief（已在 BuildUserPrompt，避免重复）；全空时返空串。
func formatDialogHistory(msgs []conversation.Message, currentBrief string) string {
	var b strings.Builder
	brief := strings.TrimSpace(currentBrief)
	for _, m := range msgs {
		content := strings.TrimSpace(m.Content)
		if content == "" || content == brief {
			continue
		}
		b.WriteString(dialogSpeaker(m.Role))
		b.WriteString("：")
		b.WriteString(content)
		b.WriteString("\n")
	}
	if b.Len() == 0 {
		return ""
	}
	return "## 对话历史（最近 " + strconv.Itoa(maxDialogHistory) + " 条内，按时间正序）\n" + b.String()
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
