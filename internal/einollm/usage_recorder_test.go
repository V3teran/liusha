package einollm

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cloudwego/eino/adk"
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

func chatModelInfo() *callbacks.RunInfo {
	return &callbacks.RunInfo{Component: components.ComponentOfChatModel}
}

func TestUsageRecorder_RecordsTokens(t *testing.T) {
	sink := &fakeSink{}
	hid, tid := "hunter-1", "task-1"
	h := NewUsageRecorder(sink,
		llm.CallMeta{HunterID: &hid, TaskID: &tid, RouteKey: "traffic-analysis"},
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
	if g.Provider != "xiaomi_mimo" || g.Model != "mimo-v2.5" || g.Role != "traffic-analysis" {
		t.Errorf("provider/model/role 错: %+v", g)
	}
	if g.HunterID == nil || *g.HunterID != "hunter-1" || g.TaskID == nil || *g.TaskID != "task-1" {
		t.Errorf("task/hunter 注入错: %+v", g)
	}
	if g.FinishReason != "stop" {
		t.Errorf("finish_reason 错: %q", g.FinishReason)
	}
	if len(g.Result) == 0 || len(g.Messages) == 0 {
		t.Errorf("审计 messages/result 应非空: msgs=%d result=%d", len(g.Messages), len(g.Result))
	}
}

func TestUsageRecorder_IgnoresNonChatModel(t *testing.T) {
	sink := &fakeSink{}
	h := NewUsageRecorder(sink, llm.CallMeta{RouteKey: "traffic-analysis"}, "p", "m")
	// 非 ChatModel 组件（如 Tool）不应触发落库
	info := &callbacks.RunInfo{Component: components.ComponentOfTool}
	ctx := h.OnStart(context.Background(), info, &model.CallbackInput{})
	h.OnEnd(ctx, info, &model.CallbackOutput{TokenUsage: &model.TokenUsage{PromptTokens: 9}})
	if sink.count != 0 {
		t.Fatalf("非 ChatModel 组件不应落库，得到 %d 行", sink.count)
	}
}

func TestUsageRecorder_RecordsFailure(t *testing.T) {
	sink := &fakeSink{}
	h := NewUsageRecorder(sink, llm.CallMeta{RouteKey: "traffic-analysis"}, "xiaomi_mimo", "mimo-v2.5")
	info := chatModelInfo()
	ctx := h.OnStart(context.Background(), info, &model.CallbackInput{})
	// ChatModel 调用失败（瞬时 400 等）→ OnError 也落库带 error
	h.OnError(ctx, info, errors.New("status code: 400, Param Incorrect"))
	if sink.count != 1 {
		t.Fatalf("失败调用应落 1 行 llm_invocation，得到 %d", sink.count)
	}
	if sink.got.Error == "" || !strings.Contains(sink.got.Error, "Param Incorrect") {
		t.Errorf("失败行应带 error: %q", sink.got.Error)
	}
	if sink.got.Provider != "xiaomi_mimo" || sink.got.Role != "traffic-analysis" {
		t.Errorf("失败行 provider/role 错: %+v", sink.got)
	}
}

// #3：Agent 边界存入的 agent 名应覆盖 meta.RouteKey，使 role 按真实产出子代理归属。
func TestUsageRecorder_RoleFromAgentBoundary(t *testing.T) {
	sink := &fakeSink{}
	h := NewUsageRecorder(sink, llm.CallMeta{RouteKey: "orchestrator"}, "p", "m")

	// 先经 Agent 边界 OnStart（exploitation 子代理）→ ctx 带 agent 名。
	agentInfo := &callbacks.RunInfo{Component: adk.ComponentOfAgent, Name: "exploitation"}
	ctx := h.OnStart(context.Background(), agentInfo, nil)
	// 其内部 ChatModel 调用沿用该 ctx。
	cm := chatModelInfo()
	ctx = h.OnStart(ctx, cm, &model.CallbackInput{})
	h.OnEnd(ctx, cm, &model.CallbackOutput{TokenUsage: &model.TokenUsage{PromptTokens: 10, CompletionTokens: 5}})

	if sink.count != 1 {
		t.Fatalf("应落 1 行")
	}
	if sink.got.Role != "exploitation" {
		t.Errorf("role 应取 Agent 边界名 exploitation，得到 %q", sink.got.Role)
	}
}
