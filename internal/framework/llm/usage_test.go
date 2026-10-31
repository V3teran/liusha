package llm

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// 审计归属链回归：ctx 里的 CallMeta（task/role/agent_run_id）必须原样到达 Sink——
// llm_invocation 的归属外键曾因 sink 拿不到 run id 而恒 NULL。

type metaStubProvider struct{ Response }

func (p *metaStubProvider) ProviderID() string { return "stub" }
func (p *metaStubProvider) ModelID() string    { return "stub-model" }
func (p *metaStubProvider) Complete(_ context.Context, _ Request) (Response, error) {
	return p.Response, nil
}
func (p *metaStubProvider) Stream(_ context.Context, _ Request) (<-chan StreamEvent, error) {
	return nil, ErrStreamingUnsupported
}
func (p *metaStubProvider) CountTokens(_ context.Context, _ Request) (int, error) {
	return 0, nil
}

type captureSink struct {
	recs []UsageRecord
}

func (s *captureSink) RecordUsage(_ context.Context, r UsageRecord) {
	s.recs = append(s.recs, r)
}

func TestInstrumentProvider_CallMetaReachesSink(t *testing.T) {
	sink := &captureSink{}
	p := InstrumentProvider(&metaStubProvider{Response: Response{
		Content: "ok",
		Usage:   Usage{InTokens: 3, OutTokens: 5},
	}}, sink)

	ctx := WithCallMeta(context.Background(), CallMeta{
		TaskID:     "task-1",
		AgentRunID: "run-9",
		Role:       "executor",
	})
	if _, err := p.Complete(ctx, Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	if len(sink.recs) != 1 {
		t.Fatalf("应落 1 行审计, got %d", len(sink.recs))
	}
	rec := sink.recs[0]
	if rec.TaskID != "task-1" || rec.Role != "executor" {
		t.Errorf("task/role 未达 sink: %+v", rec)
	}
	if rec.AgentRunID != "run-9" {
		t.Errorf("AgentRunID 未达 sink（归属外键会恒 NULL）: %+v", rec)
	}
}

func TestInstrumentProvider_NilMetaLeavesAttributionEmpty(t *testing.T) {
	sink := &captureSink{}
	p := InstrumentProvider(&metaStubProvider{Response: Response{Content: "ok"}}, sink)

	if _, err := p.Complete(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	rec := sink.recs[0]
	if rec.TaskID != "" || rec.AgentRunID != "" || rec.Role != "" {
		t.Errorf("无 CallMeta 时归属字段应为空: %+v", rec)
	}
}

// streamStubProvider 按脚本吐流式事件（text/thinking/tool_call 增量 + done 收尾）。
type streamStubProvider struct{ events []StreamEvent }

func (p *streamStubProvider) ProviderID() string { return "stub" }
func (p *streamStubProvider) ModelID() string    { return "stub-model" }
func (p *streamStubProvider) Complete(_ context.Context, _ Request) (Response, error) {
	return Response{}, nil
}
func (p *streamStubProvider) Stream(_ context.Context, _ Request) (<-chan StreamEvent, error) {
	ch := make(chan StreamEvent, len(p.events))
	for _, ev := range p.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}
func (p *streamStubProvider) CountTokens(_ context.Context, _ Request) (int, error) {
	return 0, nil
}

// 流式调用的审计 Result 影子聚合：content/thinking/tool_calls/usage 与 Complete
// 路径同构——此前流式行 result 恒 {}，审计对 agent 主路径失明。
func TestInstrumentProvider_StreamRecordsAssembledResult(t *testing.T) {
	sink := &captureSink{}
	p := InstrumentProvider(&streamStubProvider{events: []StreamEvent{
		{Kind: StreamText, Content: "先想"},
		{Kind: StreamText, Content: "后说"},
		{Kind: StreamThinking, Content: "思考增量"},
		{Kind: StreamToolCall, Tool: &ToolCall{ID: "c1", Name: "run_command", Arguments: []byte(`{"cmd":"ls"}`)}},
		{Kind: StreamDone, Usage: &Usage{InTokens: 7, OutTokens: 9}, Signature: "sig-1"},
	}}, sink)

	ch, err := p.Stream(context.Background(), Request{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	for range ch { //nolint:revive // 排空通道（审计在关闭后落库）
	}

	if len(sink.recs) != 1 {
		t.Fatalf("应落 1 行审计, got %d", len(sink.recs))
	}
	rec := sink.recs[0]
	if rec.InTokens != 7 || rec.OutTokens != 9 {
		t.Errorf("用量未达 sink: %+v", rec)
	}
	var got Response
	if err := json.Unmarshal(rec.Result, &got); err != nil {
		t.Fatalf("Result 应为 Response JSON: %v", err)
	}
	if got.Content != "先想后说" {
		t.Errorf("正文聚合 = %q", got.Content)
	}
	if got.Thinking == nil || got.Thinking.Text != "思考增量" || got.Thinking.Signature != "sig-1" {
		t.Errorf("思考聚合 = %+v", got.Thinking)
	}
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "run_command" {
		t.Errorf("工具调用聚合 = %+v", got.ToolCalls)
	}
}

// 流式以 StreamError 收尾：不写 Result（与 Complete 错误路径口径一致），错误入 FinishReason。
func TestInstrumentProvider_StreamErrorOmitsResult(t *testing.T) {
	sink := &captureSink{}
	p := InstrumentProvider(&streamStubProvider{events: []StreamEvent{
		{Kind: StreamText, Content: "部分"},
		{Kind: StreamError, Err: errors.New("boom")},
	}}, sink)

	ch, _ := p.Stream(context.Background(), Request{})
	for range ch { //nolint:revive // 排空通道（审计在关闭后落库）
	}

	rec := sink.recs[0]
	if string(rec.Result) != "" && string(rec.Result) != "{}" {
		t.Errorf("错误流不应写 Result, got %s", rec.Result)
	}
	if rec.FinishReason != "error: boom" {
		t.Errorf("FinishReason = %q", rec.FinishReason)
	}
}
