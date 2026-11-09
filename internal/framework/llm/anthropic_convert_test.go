package llm

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestToAnthropicMessages_RejectsInvalidToolArgs 锁定与 openai 协议对称的边界校验：
// 工具参数原样嵌进请求体，非法 JSON 会让整个请求体非法——在本地报出明确错误，
// 而不是发出去换一个 provider 的 400。
func TestToAnthropicMessages_RejectsInvalidToolArgs(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "计算"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "calculator", Arguments: json.RawMessage(`{"expression": "2+`)}}},
	}
	_, _, err := toAnthropicMessages(msgs)
	if err == nil || !strings.Contains(err.Error(), "不是合法 JSON") {
		t.Fatalf("期望报参数非法，得 %v", err)
	}

	msgs[1].ToolCalls[0].Arguments = json.RawMessage(`{"expression": "2+2"}`)
	if _, _, err := toAnthropicMessages(msgs); err != nil {
		t.Fatalf("合法参数不应报错: %v", err)
	}
}
