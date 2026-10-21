package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// TestRun_UsageAccumulatesAcrossIterations 验证：每步 token/耗时的数据源——
// ReActResult.Usage 逐迭代累计（含工具循环里的全部 LLM 调用）、Model 取绑定
// provider、ElapsedMs 覆盖 Run 墙钟。时间线每步 chip 消费这三个字段。
func TestRun_UsageAccumulatesAcrossIterations(t *testing.T) {
	provider := &mockLLMProvider{responses: []llm.Response{
		{
			Content:   "先查一下",
			ToolCalls: []llm.ToolCall{{ID: "c1", Name: "probe", Arguments: json.RawMessage(`{"t":"x"}`)}},
			Usage:     llm.Usage{InTokens: 10, OutTokens: 4},
		},
		{
			// 结构化终稿：不触发强制补写（该路径另有独立测试），保持纯迭代累计口径
			Content: `{"conclusion":"目标可达"}`,
			Usage:   llm.Usage{InTokens: 20, OutTokens: 6},
		},
	}}
	tool := &mockTool{name: "probe", description: "探测", handler: func(json.RawMessage) (any, error) {
		return "ok", nil
	}}
	rt := NewReActRuntime()
	if err := rt.RegisterTool(tool); err != nil {
		t.Fatalf("注册工具: %v", err)
	}

	result, err := rt.Run(context.Background(), &ReActConfig{
		LLMProvider: provider,
		MaxIterations: 5,
		Objective:     "hi",
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}

	if got := result.Usage; got.InTokens != 30 || got.OutTokens != 10 {
		t.Fatalf("Usage 应跨迭代累计 = %+v, want {30 10}", got)
	}
	if result.Model != "test-model" {
		t.Fatalf("Model = %q, want test-model", result.Model)
	}
	if result.ElapsedMs < 0 {
		t.Fatalf("ElapsedMs = %d, want >= 0", result.ElapsedMs)
	}
}
