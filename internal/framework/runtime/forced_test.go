package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// 收尾路径（终稿没通过验收 → forceFinalAnswer）的 LLM 调用绕过 callLLM 直调
// Complete——其 usage 必须同样累进 ReActResult.Usage，否则时间线每步 chip 漏计
// （实测出现过整轮双 0 的空正文卡）。
func TestRun_ForcedOutputUsageAccumulates(t *testing.T) {
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{
			// 迭代 1：模型自然停止，终稿是一段说明、没通过验收 → 触发收尾
			Content:  "先摸清对象面再深挖。",
			Usage:    llm.Usage{InTokens: 2802, OutTokens: 1487},
			Thinking: &llm.ThinkingBlock{Text: "长思考"},
		},
		{
			// 收尾调用：返回合格的终稿
			Content: `{"should_continue":false}`,
			Usage:   llm.Usage{InTokens: 4464, OutTokens: 2136},
		},
	}}}

	result, err := NewReActRuntime().Run(context.Background(), &ReActConfig{
		LLMProvider:        provider,
		MaxIterations:      3,
		Objective:          "hi",
		OutputFormatPrompt: "规划者",
		OutputCheck:        planCheck,
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}

	if got := result.Usage; got.InTokens != 2802+4464 || got.OutTokens != 1487+2136 {
		t.Fatalf("收尾调用的 usage 应累进 = %+v, want {7266 3623}", got)
	}
	if len(provider.requests) != 2 {
		t.Fatalf("应当是一次迭代加一次收尾 = 2 次调用, got %d", len(provider.requests))
	}
	if result.FinalAnswer != `{"should_continue":false}` {
		t.Fatalf("FinalAnswer = %q", result.FinalAnswer)
	}
}

// 迭代路径与收尾路径混合累计（工具迭代 + 末轮没通过验收 + 收尾）。
func TestRun_MixedIterationsAndForcedUsage(t *testing.T) {
	provider := &recordingProvider{mockLLMProvider: mockLLMProvider{responses: []llm.Response{
		{
			Content:   "先查一下",
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{}`)}},
			Usage:     llm.Usage{InTokens: 10, OutTokens: 4},
		},
		{
			// 末轮：自然停止但终稿没通过验收 → 收尾
			Content: "查完了。",
			Usage:   llm.Usage{InTokens: 20, OutTokens: 6},
		},
		{
			Content: `{"should_continue":true}`,
			Usage:   llm.Usage{InTokens: 30, OutTokens: 8},
		},
	}}}
	rt := NewReActRuntime()
	if err := rt.RegisterTool(&mockTool{name: "probe", description: "探测", handler: func(json.RawMessage) (any, error) {
		return "obs", nil
	}}); err != nil {
		t.Fatalf("注册工具: %v", err)
	}

	result, err := rt.Run(context.Background(), &ReActConfig{
		LLMProvider:        provider,
		MaxIterations:      5,
		Objective:          "hi",
		OutputFormatPrompt: "x",
		OutputCheck:        planCheck,
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if got := result.Usage; got.InTokens != 60 || got.OutTokens != 18 {
		t.Fatalf("混合路径累计 = %+v, want {60 18}", got)
	}
	if len(provider.requests) != 3 {
		t.Fatalf("应当是两次迭代加一次收尾 = 3 次调用, got %d", len(provider.requests))
	}
	if result.FinalAnswer != `{"should_continue":true}` {
		t.Fatalf("FinalAnswer = %q", result.FinalAnswer)
	}
}
