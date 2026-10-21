package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 连续工具失败止损：达到装配阈值即终止本轮 ReAct（单机制——中间件的重复
// 分支已删除，此处锁定唯一实现的行为）。
func TestRun_ConsecutiveToolFailureStopLoss(t *testing.T) {
	provider := &mockLLMProvider{responses: []llm.Response{
		{Content: "试一下", ToolCalls: []llm.ToolCall{{ID: "c1", Name: "boom", Arguments: json.RawMessage(`{}`)}}},
		{Content: "再试", ToolCalls: []llm.ToolCall{{ID: "c2", Name: "boom", Arguments: json.RawMessage(`{}`)}}},
		{Content: "还试", ToolCalls: []llm.ToolCall{{ID: "c3", Name: "boom", Arguments: json.RawMessage(`{}`)}}},
		{Content: "终稿"},
	}}
	boom := &mockTool{name: "boom", description: "必失败", handler: func(json.RawMessage) (any, error) {
		return nil, errors.New("always fails")
	}}
	rt := NewReActRuntime()
	if err := rt.RegisterTool(boom); err != nil {
		t.Fatalf("注册工具: %v", err)
	}

	result, err := rt.Run(context.Background(), &ReActConfig{
		LLMProvider:                provider,
		MaxIterations:              20,
		MaxConsecutiveToolFailures: 2, // 第 2 次连续失败即止损
		Objective:                  "hi",
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	// 每轮 1 次失败工具调用：第 2 轮迭代末计数达 2 → 止损终止，不该跑到第 4 轮。
	if result.Iterations > 3 {
		t.Fatalf("连续失败应在阈值处止损, got %d iterations", result.Iterations)
	}
	// 止损注入的说明消息应存在于历史（把已收集观察交回上层）。
	found := false
	for _, m := range result.MessageHistory {
		if m.Role == llm.RoleUser && strings.Contains(m.Content, "止损") {
			found = true
		}
	}
	if !found {
		t.Fatal("止损应注入说明消息")
	}
}
