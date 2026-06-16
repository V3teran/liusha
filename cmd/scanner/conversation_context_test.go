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
