package cognition

import (
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 蒸馏请求必须声明优先：有了账户在途上限后，账户满载时预算短的调用排不到名额就作废，
// 而蒸馏作废意味着被裁掉的历史不留纪要。
func TestDistillRequestTakesPriorityAndFoldsPriorSummary(t *testing.T) {
	evicted := []llm.Message{
		{Role: llm.RoleUser, Content: "检查登录页"},
		{Role: llm.RoleAssistant, Content: "发现表单缺少令牌"},
	}
	req := distillRequest("executor", evicted, "上一份纪要正文")
	if !req.Priority {
		t.Fatal("蒸馏请求应优先取得在途名额")
	}
	if len(req.Messages) != 2 || req.Messages[0].Role != llm.RoleSystem || req.Messages[1].Role != llm.RoleUser {
		t.Fatalf("蒸馏请求应是「系统提示词 + 一条用户消息」，实际 %+v", req.Messages)
	}
	user := req.Messages[1].Content
	if !strings.Contains(user, "executor") || !strings.Contains(user, "发现表单缺少令牌") {
		t.Fatalf("蒸馏请求应带上角色与被裁的历史，实际 %q", user)
	}
	if !strings.Contains(user, "上一份纪要正文") {
		t.Fatal("上一份纪要应折叠进新的蒸馏请求")
	}
	if again := distillRequest("executor", evicted, ""); strings.Contains(again.Messages[1].Content, "上一份纪要") {
		t.Fatal("没有上一份纪要时不应出现折叠段落")
	}
}
