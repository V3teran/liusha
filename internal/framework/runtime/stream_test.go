package runtime

import (
	"context"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// streamingProvider 是流式路径的 LLM 替身：把一段文本按块走 Stream 事件通道。
type streamingProvider struct {
	mockLLMProvider
	chunks []string
}

func (p *streamingProvider) Stream(_ context.Context, _ llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, len(p.chunks)+1)
	for _, c := range p.chunks {
		ch <- llm.StreamEvent{Kind: llm.StreamText, Content: c}
	}
	ch <- llm.StreamEvent{Kind: llm.StreamDone, Usage: &llm.Usage{InTokens: 1, OutTokens: 2}}
	close(ch)
	return ch, nil
}

// TestRun_ThoughtDeltaStreaming 验证：装配 OnThoughtDelta 时走流式路径，
// 每个 text 增量即时回调；聚合结果与非流式同构（OnThought 拿到全文）。
func TestRun_ThoughtDeltaStreaming(t *testing.T) {
	provider := &streamingProvider{
		mockLLMProvider: mockLLMProvider{responses: []llm.Response{{Content: `{"ok":true}`}}},
		chunks:          []string{"探测 ", "目标 ", "完成"},
	}
	var deltas []string
	full := ""
	rt := NewReActRuntime()
	_, err := rt.Run(context.Background(), &ReActConfig{
		LLMProvider:   provider,
		MaxIterations: 1,
		OnThoughtDelta: func(chunk string) {
			deltas = append(deltas, chunk)
		},
		OnThought: func(thought string) { full = thought },
		Objective: "hi",
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if len(deltas) != 3 || deltas[0] != "探测 " || deltas[2] != "完成" {
		t.Fatalf("增量回调 = %v, want 3 chunks", deltas)
	}
	if full != "探测 目标 完成" {
		t.Fatalf("整段 OnThought = %q", full)
	}
}

// TestRun_ThoughtDeltaFallback 验证：Provider 无流式能力时降级 Complete，
// 不产生增量回调、结果照常返回。
func TestRun_ThoughtDeltaFallback(t *testing.T) {
	provider := &mockLLMProvider{responses: []llm.Response{
		{Content: "整段回答"},
	}}
	var deltas []string
	rt := NewReActRuntime()
	_, err := rt.Run(context.Background(), &ReActConfig{
		LLMProvider:   provider,
		MaxIterations: 1,
		OnThoughtDelta: func(chunk string) {
			deltas = append(deltas, chunk)
		},
		Objective: "hi",
	})
	if err != nil {
		t.Fatalf("Run err: %v", err)
	}
	if len(deltas) != 0 {
		t.Fatalf("降级路径不应有增量回调, got %v", deltas)
	}
}
