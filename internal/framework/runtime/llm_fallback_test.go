package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/framework/llm"
)

// midStreamErrProvider 的流式路径先吐一段增量，再推一个错误事件（模拟上游在生成途中
// 推来限流 / 过载）；Complete 路径返回一个可用响应并计数——用于验证中途瞬时故障的降级。
type midStreamErrProvider struct {
	mockLLMProvider
	streamErr     error
	completeResp  llm.Response
	completeCalls int
}

func (p *midStreamErrProvider) Stream(_ context.Context, _ llm.Request) (<-chan llm.StreamEvent, error) {
	ch := make(chan llm.StreamEvent, 2)
	ch <- llm.StreamEvent{Kind: llm.StreamText, Content: "部分思考"}
	ch <- llm.StreamEvent{Kind: llm.StreamError, Err: p.streamErr}
	close(ch)
	return ch, nil
}

func (p *midStreamErrProvider) Complete(_ context.Context, _ llm.Request) (llm.Response, error) {
	p.completeCalls++
	return p.completeResp, nil
}

// 流式消费途中遇可重试的瞬时故障：整体降级为一次非流式 Complete（由重试装饰器重试），
// 本轮照常拿到响应，不把失败抛给调用方——保全本轮已累积的推理，不必整段重做。
func TestCallLLMStreaming_TransientMidStreamFallsBackToComplete(t *testing.T) {
	provider := &midStreamErrProvider{
		streamErr:    &llm.HTTPError{Code: 429},
		completeResp: llm.Response{Content: "降级后的响应"},
	}
	cfg := &ReActConfig{LLMProvider: provider, OnThoughtDelta: func(string) {}}
	resp, err := (&reactRuntime{}).callLLMStreaming(context.Background(), cfg, llm.Request{})
	if err != nil {
		t.Fatalf("瞬时中途故障应降级重试、不报错，得 %v", err)
	}
	if provider.completeCalls != 1 {
		t.Fatalf("应降级调用一次 Complete，实际 %d 次", provider.completeCalls)
	}
	if resp.Content != "降级后的响应" {
		t.Fatalf("响应应取自降级 Complete，得 %q", resp.Content)
	}
}

// 流式途中的非瞬时错误（如协议不符）不降级、原样上抛：不把真问题伪装成可重试。
func TestCallLLMStreaming_NonTransientMidStreamPropagates(t *testing.T) {
	provider := &midStreamErrProvider{
		streamErr:    errors.New("响应不符合所选协议"),
		completeResp: llm.Response{Content: "不应被调用"},
	}
	cfg := &ReActConfig{LLMProvider: provider, OnThoughtDelta: func(string) {}}
	_, err := (&reactRuntime{}).callLLMStreaming(context.Background(), cfg, llm.Request{})
	if err == nil {
		t.Fatal("非瞬时中途错误应上抛，got nil")
	}
	if provider.completeCalls != 0 {
		t.Fatalf("非瞬时错误不应降级 Complete，实际 %d 次", provider.completeCalls)
	}
}
