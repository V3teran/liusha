package einollm

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/callbacks"
	"github.com/cloudwego/eino/components"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/llminvocation"
)

type fakeSink struct {
	got   llminvocation.Invocation
	count int
}

func (f *fakeSink) Append(_ context.Context, c llminvocation.Invocation) (int64, error) {
	f.got = c
	f.count++
	return int64(f.count), nil
}

type fakePricing struct{ perCall float64 }

func (f fakePricing) Estimate(_, _ string, _ llm.Usage) float64 { return f.perCall }

func chatModelInfo() *callbacks.RunInfo {
	return &callbacks.RunInfo{Component: components.ComponentOfChatModel}
}

func TestUsageRecorder_RecordsTokensAndCost(t *testing.T) {
	sink := &fakeSink{}
	hid, ot, oid := "hunter-1", "passive_session", "owner-1"
	h := NewUsageRecorder(sink, fakePricing{perCall: 0.42},
		llm.CallMeta{HunterID: &hid, OwnerType: &ot, OwnerID: &oid, RouteKey: "tracker"},
		"xiaomi_mimo", "mimo-v2.5",
	)

	info := chatModelInfo()
	ctx := h.OnStart(context.Background(), info, &model.CallbackInput{
		Messages: []*schema.Message{schema.UserMessage("hi")},
	})
	h.OnEnd(ctx, info, &model.CallbackOutput{
		Config: &model.Config{Model: "mimo-v2.5"},
		Message: &schema.Message{
			Role:         schema.Assistant,
			Content:      "done",
			ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"},
		},
		TokenUsage: &model.TokenUsage{
			PromptTokens:       100,
			CompletionTokens:   50,
			PromptTokenDetails: model.PromptTokenDetails{CachedTokens: 20},
		},
	})

	if sink.count != 1 {
		t.Fatalf("应落 1 行 llm_invocation，得到 %d", sink.count)
	}
	g := sink.got
	if g.InTokens != 100 || g.OutTokens != 50 || g.CachedTokens != 20 {
		t.Errorf("token 映射错: %+v", g)
	}
	if g.Provider != "xiaomi_mimo" || g.Model != "mimo-v2.5" || g.Role != "tracker" {
		t.Errorf("provider/model/role 错: %+v", g)
	}
	if g.HunterID == nil || *g.HunterID != "hunter-1" || g.OwnerID == nil || *g.OwnerID != "owner-1" {
		t.Errorf("owner/hunter 注入错: %+v", g)
	}
	if g.FinishReason != "stop" {
		t.Errorf("finish_reason 错: %q", g.FinishReason)
	}
	if g.CostUSD != 0.42 {
		t.Errorf("cost 错: %v", g.CostUSD)
	}
	if len(g.Result) == 0 || len(g.Messages) == 0 {
		t.Errorf("审计 messages/result 应非空: msgs=%d result=%d", len(g.Messages), len(g.Result))
	}
}

func TestUsageRecorder_IgnoresNonChatModel(t *testing.T) {
	sink := &fakeSink{}
	h := NewUsageRecorder(sink, nil, llm.CallMeta{RouteKey: "tracker"}, "p", "m")
	// 非 ChatModel 组件（如 Tool）不应触发落库
	info := &callbacks.RunInfo{Component: components.ComponentOfTool}
	ctx := h.OnStart(context.Background(), info, &model.CallbackInput{})
	h.OnEnd(ctx, info, &model.CallbackOutput{TokenUsage: &model.TokenUsage{PromptTokens: 9}})
	if sink.count != 0 {
		t.Fatalf("非 ChatModel 组件不应落库，得到 %d 行", sink.count)
	}
}

func TestUsageRecorder_NilPricingNoCost(t *testing.T) {
	sink := &fakeSink{}
	h := NewUsageRecorder(sink, nil, llm.CallMeta{RouteKey: "tracker"}, "p", "m")
	info := chatModelInfo()
	ctx := h.OnStart(context.Background(), info, &model.CallbackInput{})
	h.OnEnd(ctx, info, &model.CallbackOutput{TokenUsage: &model.TokenUsage{PromptTokens: 10, CompletionTokens: 5}})
	if sink.count != 1 {
		t.Fatalf("应落 1 行")
	}
	if sink.got.CostUSD != 0 {
		t.Errorf("nil pricing 应 cost=0，得到 %v", sink.got.CostUSD)
	}
}
