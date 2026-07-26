package einollm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

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

// 非流式：一次性返回，首 token 即末 token → TTFT 无意义应留 0，且不标 is_stream。
func TestUsageRecorder_NonStreamHasNoTTFT(t *testing.T) {
	sink := &fakeSink{}
	h := NewUsageRecorder(sink, llm.CallMeta{RouteKey: "orchestrator"}, "p", "m")
	info := chatModelInfo()
	ctx := h.OnStart(context.Background(), info, &model.CallbackInput{})
	h.OnEnd(ctx, info, &model.CallbackOutput{TokenUsage: &model.TokenUsage{PromptTokens: 10, CompletionTokens: 5}})

	if sink.got.IsStream {
		t.Error("非流式调用不该标 is_stream")
	}
	if sink.got.TTFTMs != 0 {
		t.Errorf("非流式 TTFT 应为 0，得到 %d", sink.got.TTFTMs)
	}
}

// 流式：应标 is_stream 且测出 TTFT（首个有文字的 chunk 到达时刻 - 调用发起）。
// 用两个 chunk 中间插延时，断言 TTFT 落在首 chunk 而非末 chunk。
func TestUsageRecorder_StreamMeasuresTTFT(t *testing.T) {
	sink := &fakeSink{}
	h := NewUsageRecorder(sink, llm.CallMeta{RouteKey: "orchestrator"}, "p", "m")
	info := chatModelInfo()
	ctx := h.OnStart(context.Background(), info, &model.CallbackInput{})

	sr, sw := schema.Pipe[callbacks.CallbackOutput](2)
	go func() {
		defer sw.Close()
		// 首个有文字的 chunk：TTFT 锚点
		sw.Send(&model.CallbackOutput{Message: &schema.Message{Role: schema.Assistant, Content: "he"}}, nil)
		// 末 chunk 明显更晚；若 TTFT 误取末 chunk，下面 TTFT≈总时长 的断言会失败
		time.Sleep(120 * time.Millisecond)
		sw.Send(&model.CallbackOutput{
			Message:    &schema.Message{Role: schema.Assistant, Content: "llo", ResponseMeta: &schema.ResponseMeta{FinishReason: "stop"}},
			TokenUsage: &model.TokenUsage{PromptTokens: 10, CompletionTokens: 5},
		}, nil)
	}()

	h.OnEndWithStreamOutput(ctx, info, sr)
	// 落库在 goroutine 里，等它跑完
	deadline := time.Now().Add(3 * time.Second)
	for sink.count == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	g := sink.got
	if sink.count != 1 {
		t.Fatalf("流式调用应落 1 行，得到 %d", sink.count)
	}
	if !g.IsStream {
		t.Error("流式调用应标 is_stream")
	}
	if g.TTFTMs <= 0 {
		t.Errorf("流式应测出 TTFT，得到 %d", g.TTFTMs)
	}
	// TTFT 是首 chunk，总时长含 120ms 等待——两者必须明显拉开，否则说明取的是末 chunk。
	if g.LatencyMs-g.TTFTMs < 80 {
		t.Errorf("TTFT 应锚在首 chunk（远早于末 chunk）：ttft=%d latency=%d", g.TTFTMs, g.LatencyMs)
	}
	if g.OutTokens != 5 {
		t.Errorf("流式 token 应取末 chunk usage，得到 out=%d", g.OutTokens)
	}
	if g.FinishReason != "stop" {
		t.Errorf("流式 finish_reason 应取末 chunk，得到 %q", g.FinishReason)
	}
}

// 纯 tool_call 流（整个流无文字内容）：TTFT 退化为首个非 nil chunk，否则这类调用永远测不到。
func TestUsageRecorder_StreamTTFTFallsBackWhenNoText(t *testing.T) {
	sink := &fakeSink{}
	h := NewUsageRecorder(sink, llm.CallMeta{RouteKey: "orchestrator"}, "p", "m")
	info := chatModelInfo()
	ctx := h.OnStart(context.Background(), info, &model.CallbackInput{})

	sr, sw := schema.Pipe[callbacks.CallbackOutput](1)
	go func() {
		defer sw.Close()
		// 无 Content，只有 tool_calls（orchestrator 派活的典型形态：实测占 93%）
		sw.Send(&model.CallbackOutput{
			Message: &schema.Message{
				Role:      schema.Assistant,
				ToolCalls: []schema.ToolCall{{ID: "c1", Function: schema.FunctionCall{Name: "task"}}},
			},
			TokenUsage: &model.TokenUsage{PromptTokens: 8},
		}, nil)
	}()

	h.OnEndWithStreamOutput(ctx, info, sr)
	deadline := time.Now().Add(3 * time.Second)
	for sink.count == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}

	if sink.count != 1 {
		t.Fatalf("应落 1 行，得到 %d", sink.count)
	}
	if !sink.got.IsStream {
		t.Error("应标 is_stream")
	}
	if sink.got.TTFTMs <= 0 {
		t.Errorf("无文字的流也应测出 TTFT（退化为首个 chunk），得到 %d", sink.got.TTFTMs)
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
