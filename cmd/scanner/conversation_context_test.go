package main

import (
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/conversation"
)

func msg(role conversation.Role, content string) conversation.Message {
	return conversation.Message{Role: role, Kind: conversation.KindMessage, Content: content}
}

// TestFormatDialogHistory_SkipsBriefAndEmpty 锁住：跳过本轮 brief（防重复）+ 空内容，保留其余、带角色标签。
func TestFormatDialogHistory_SkipsBriefAndEmpty(t *testing.T) {
	msgs := []conversation.Message{
		msg(conversation.RoleUser, "测试这个登录接口"),
		msg(conversation.RoleAssistant, "发现一个 SQL 注入"),
		msg(conversation.RoleUser, "  "),       // 空内容，跳过
		msg(conversation.RoleUser, "刚才那个怎么利用"), // 本轮 brief，跳过
	}
	out := formatDialogHistory(msgs, "刚才那个怎么利用")

	if strings.Contains(out, "刚才那个怎么利用") {
		t.Error("本轮 brief 应被跳过（避免与 BuildUserPrompt 重复）")
	}
	if !strings.Contains(out, "用户：测试这个登录接口") {
		t.Error("应保留历史用户消息 + 角色标签")
	}
	if !strings.Contains(out, "助手：发现一个 SQL 注入") {
		t.Error("应保留历史助手消息 + 角色标签")
	}
	if strings.Count(out, "\n") < 2 { // 至少 2 条保留 + 标题
		t.Errorf("空内容应被跳过，输出行数异常：%q", out)
	}
}

// TestFormatDialogHistory_AllSkipped 全部被跳过时返空串（首轮：只有本轮 brief）。
func TestFormatDialogHistory_AllSkipped(t *testing.T) {
	msgs := []conversation.Message{msg(conversation.RoleUser, "扫这个站")}
	if got := formatDialogHistory(msgs, "扫这个站"); got != "" {
		t.Errorf("仅含本轮 brief 时应返空串，实得：%q", got)
	}
	if got := formatDialogHistory(nil, "x"); got != "" {
		t.Errorf("无消息时应返空串，实得：%q", got)
	}
}

// TestSplitDialogByBudget_AllWithinBudget：总 token 不超预算时全归 recent，older 为空。
func TestSplitDialogByBudget_AllWithinBudget(t *testing.T) {
	msgs := []conversation.Message{
		msg(conversation.RoleUser, "a"),
		msg(conversation.RoleAssistant, "b"),
	}
	older, recent := splitDialogByBudget(msgs, 1000)
	if len(older) != 0 {
		t.Errorf("预算充足时 older 应为空，得 %d 条", len(older))
	}
	if len(recent) != 2 {
		t.Errorf("预算充足时 recent 应含全部 2 条，得 %d", len(recent))
	}
}

// TestSplitDialogByBudget_SplitsByToken：超预算时最近的归 recent、更早的归 older，保持时间正序。
func TestSplitDialogByBudget_SplitsByToken(t *testing.T) {
	// 每条 content 40 字符 ≈ 10 token；预算 15 → 只容得下最近 1 条。
	c := strings.Repeat("x", 40)
	msgs := []conversation.Message{
		msg(conversation.RoleUser, c+"-old"), // 最早
		msg(conversation.RoleAssistant, c+"-mid"),
		msg(conversation.RoleUser, c+"-new"), // 最近
	}
	older, recent := splitDialogByBudget(msgs, 15)
	if len(recent) == 0 || len(older) == 0 {
		t.Fatalf("应一分为二，得 older=%d recent=%d", len(older), len(recent))
	}
	// recent 应是最近的（含 -new），older 应是更早的（含 -old），且时间正序保持。
	if !strings.Contains(recent[len(recent)-1].Content, "-new") {
		t.Error("recent 末条应是最近消息（-new）")
	}
	if !strings.Contains(older[0].Content, "-old") {
		t.Error("older 首条应是最早消息（-old）")
	}
}

// TestSplitDialogByBudget_ZeroBudget：预算<=0（provider 未配窗口）降级为全 recent、不蒸馏。
func TestSplitDialogByBudget_ZeroBudget(t *testing.T) {
	msgs := []conversation.Message{msg(conversation.RoleUser, "a"), msg(conversation.RoleUser, "b")}
	older, recent := splitDialogByBudget(msgs, 0)
	if len(older) != 0 || len(recent) != 2 {
		t.Errorf("预算<=0 应全归 recent，得 older=%d recent=%d", len(older), len(recent))
	}
}

// TestFilterDialog_SkipsEmptyAndBrief：过滤空内容与本轮 brief，保留其余。
func TestFilterDialog_SkipsEmptyAndBrief(t *testing.T) {
	msgs := []conversation.Message{
		msg(conversation.RoleUser, "保留我"),
		msg(conversation.RoleUser, "  "),   // 空，跳
		msg(conversation.RoleUser, "本轮问题"), // brief，跳
	}
	kept := filterDialog(msgs, "本轮问题")
	if len(kept) != 1 || kept[0].Content != "保留我" {
		t.Errorf("应仅保留非空非 brief 的 1 条，得 %d 条", len(kept))
	}
}
