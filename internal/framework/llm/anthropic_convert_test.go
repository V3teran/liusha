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

// 并行工具调用的结果必须放在紧随其后的同一条 user 消息里：协议要求 assistant 的每个
// tool_use 都在「下一条消息」里有对应的 tool_result，分成多条 user 消息时只有第一条算数，
// 严格实现协议的兼容端点据此拒绝请求（400）。
func TestToAnthropicMessages_ParallelToolResultsShareOneUserMessage(t *testing.T) {
	msgs := []Message{
		{Role: RoleUser, Content: "规划"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{
			{ID: "call_a", Name: "observe_state", Arguments: json.RawMessage(`{}`)},
			{ID: "call_b", Name: "search_corpus", Arguments: json.RawMessage(`{"query":"xss"}`)},
		}},
		{Role: RoleTool, ToolCallID: "call_a", Content: "state"},
		{Role: RoleTool, ToolCallID: "call_b", Content: "corpus"},
		{Role: RoleUser, Content: "继续"},
	}
	_, out, err := toAnthropicMessages(msgs)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 4 {
		t.Fatalf("应为 user / assistant / user(两个 tool_result) / user，得 %d 条", len(out))
	}
	results := out[2].Content
	if len(results) != 2 || results[0].OfToolResult == nil || results[1].OfToolResult == nil ||
		results[0].OfToolResult.ToolUseID != "call_a" || results[1].OfToolResult.ToolUseID != "call_b" {
		t.Fatalf("两个工具结果应按序在同一条 user 消息里: %+v", results)
	}
	if out[3].Content[0].OfText == nil || out[3].Content[0].OfText.Text != "继续" {
		t.Fatal("工具结果之后的普通 user 消息应另起一条")
	}
}
