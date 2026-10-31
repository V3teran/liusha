package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 强制补写路径（自然停止但非结构化 → forceStructuredOutput）的 LLM 调用
// 绕过 callLLM 直调 Complete——其 usage 必须同样累进 ReActResult.Usage，
// 否则时间线每步 chip 漏计（实测出现过整轮双 0 的空正文卡）。
func TestRun_ForcedOutputUsageAccumulates(t *testing.T) {
	provider := &mockLLMProvider{responses: []llm.Response{
		{
			// 迭代 1：自然停止，Content 为空且非结构化 → 触发强制补写
			Content:  "",
			Usage:    llm.Usage{InTokens: 2802, OutTokens: 1487},
			Thinking: &llm.ThinkingBlock{Text: "长思考（模型把输出预算全花在思考上，正文为零）"},
		},
		{
			// 强制补写调用：返回结构化内容
			Content: `{"should_continue":false}`,
			Usage:   llm.Usage{InTokens: 4464, OutTokens: 2136},
		},
	}}
	rt := NewReActRuntime()

	result, err := rt.Run(context.Background(), &ReActConfig{
		LLMProvider:        provider,
		MaxIterations:      3,
		Objective:          "hi",
		OutputFormatPrompt: "规划者",
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}

	if got := result.Usage; got.InTokens != 2802+4464 || got.OutTokens != 1487+2136 {
		t.Fatalf("强制补写的 usage 应累进 = %+v, want {7266 3623}", got)
	}
	if result.FinalAnswer != `{"should_continue":false}` {
		t.Fatalf("FinalAnswer = %q", result.FinalAnswer)
	}
}

// 迭代路径与强制补写路径混合累计（工具迭代 + 末轮非结构化 + 强制补写）。
func TestRun_MixedIterationsAndForcedUsage(t *testing.T) {
	provider := &mockLLMProvider{responses: []llm.Response{
		{
			Content:   "先查一下",
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)}},
			Usage:     llm.Usage{InTokens: 10, OutTokens: 4},
		},
		{
			Content: "",
			Usage:   llm.Usage{InTokens: 20, OutTokens: 6},
		},
		{
			Content: `{"ok":true}`,
			Usage:   llm.Usage{InTokens: 30, OutTokens: 8},
		},
	}}
	tool := &mockTool{name: "probe", description: "探测", handler: func(json.RawMessage) (any, error) {
		return "obs", nil
	}}
	rt := NewReActRuntime()
	if err := rt.RegisterTool(tool); err != nil {
		t.Fatalf("注册工具: %v", err)
	}

	result, err := rt.Run(context.Background(), &ReActConfig{
		LLMProvider:        provider,
		MaxIterations:      5,
		Objective:          "hi",
		OutputFormatPrompt: "x",
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if got := result.Usage; got.InTokens != 60 || got.OutTokens != 18 {
		t.Fatalf("混合路径累计 = %+v, want {60 18}", got)
	}
}
