package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
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

// providerFuncs 按需注入 Stream/Complete 的测试替身（其余走零值）。
type providerFuncs struct {
	stream   func() (<-chan StreamEvent, error)
	complete func() (Response, error)
}

func (p providerFuncs) ProviderID() string { return "stub" }
func (p providerFuncs) ModelID() string    { return "stub-model" }
func (p providerFuncs) Complete(_ context.Context, _ Request) (Response, error) {
	if p.complete != nil {
		return p.complete()
	}
	return Response{}, nil
}
func (p providerFuncs) Stream(_ context.Context, _ Request) (<-chan StreamEvent, error) {
	return p.stream()
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
		{Kind: StreamToolCall, Tool: &ToolCall{ID: "c1", Name: "command_run", Arguments: []byte(`{"cmd":"ls"}`)}},
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
	if len(got.ToolCalls) != 1 || got.ToolCalls[0].Name != "command_run" {
		t.Errorf("工具调用聚合 = %+v", got.ToolCalls)
	}
}

// 消费方经 ctx 取消、provider 未发收尾事件即关通道：审计按取消归因（abandoned），
// 不得记成流截断——截断意味着 provider 掉流，排障口径不同。
func TestInstrumentProvider_CanceledStreamRecordsAbandonedNotTruncated(t *testing.T) {
	sink := &captureSink{}
	ctx, cancel := context.WithCancel(context.Background())
	// 受控 provider：先吐一个事件，等消费方取消后再关通道（无收尾事件）——保证
	// 审计收尾时 ctx 已取消，排除「通道先关、取消后到」的竞态。
	gated := make(chan StreamEvent)
	inner := providerFuncs{stream: func() (<-chan StreamEvent, error) {
		go func() {
			gated <- StreamEvent{Kind: StreamText, Content: "部分"}
			<-ctx.Done()
			close(gated)
		}()
		return gated, nil
	}}
	p := InstrumentProvider(inner, sink)

	ch, _ := p.Stream(ctx, Request{})
	<-ch           // 读到首个事件
	cancel()       //nolint:revive // 模拟消费方放弃
	for range ch { //nolint:revive // 排空通道（审计在关闭后落库）
	}

	rec := sink.recs[0]
	if rec.FinishReason != "error: stream abandoned: context canceled" {
		t.Errorf("FinishReason = %q，应为 abandoned 归因", rec.FinishReason)
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

// drainStream 建流并读到通道关闭（审计在关闭后落库）；建流即败时没有通道可读。
func drainStream(p Provider, ctx context.Context) {
	ch, err := p.Stream(ctx, Request{})
	if err != nil {
		return
	}
	for range ch { //nolint:revive // 排空通道（审计在关闭后落库）
	}
}

// 审计记录携带对真实错误的分类，落库一方直接采用——只凭错误文本，本地的在途名额节流
// 这类哨兵错误认不出来，会被判成「其它错误」。四条失败路径都要带：Complete 报错、建流
// 即败、流上出错、消费方放弃。
func TestInstrumentProvider_RecordsErrorClassOfRealError(t *testing.T) {
	busy := &heldError{err: fmt.Errorf("%w（上限 4）：排队未果，没有请求发出", ErrInflightBusy)}

	t.Run("Complete 报错", func(t *testing.T) {
		for _, tc := range []struct {
			name string
			err  error
			want ErrorClass
		}{
			{"排不到在途名额", busy, ClassBusy},
			{"上游限流", &HTTPError{Code: 429}, Class429},
			{"调用超时", context.DeadlineExceeded, ClassNet},
			{"其它错误", errors.New("boom"), ClassOther},
		} {
			t.Run(tc.name, func(t *testing.T) {
				sink := &captureSink{}
				p := InstrumentProvider(providerFuncs{complete: func() (Response, error) {
					return Response{}, tc.err
				}}, sink)
				_, _ = p.Complete(context.Background(), Request{})
				if got := sink.recs[0].ErrClass; got != tc.want {
					t.Fatalf("分类应为 %v，实际 %v（错误文本 %q）", tc.want, got, sink.recs[0].Err)
				}
			})
		}
	})

	t.Run("建流即败", func(t *testing.T) {
		sink := &captureSink{}
		p := InstrumentProvider(providerFuncs{stream: func() (<-chan StreamEvent, error) {
			return nil, busy
		}}, sink)
		drainStream(p, context.Background())
		if got := sink.recs[0].ErrClass; got != ClassBusy {
			t.Fatalf("分类应为 %v，实际 %v", ClassBusy, got)
		}
	})

	t.Run("流上出错", func(t *testing.T) {
		sink := &captureSink{}
		p := InstrumentProvider(&streamStubProvider{events: []StreamEvent{
			{Kind: StreamError, Err: &HTTPError{Code: 529}},
		}}, sink)
		drainStream(p, context.Background())
		if got := sink.recs[0].ErrClass; got != Class529 {
			t.Fatalf("分类应为 %v，实际 %v", Class529, got)
		}
	})

	t.Run("消费方放弃", func(t *testing.T) {
		sink := &captureSink{}
		p := InstrumentProvider(&streamStubProvider{events: []StreamEvent{{Kind: StreamText, Content: "部分"}}}, sink)
		// 截止时间已过的 ctx：放弃的原因是超时（瞬时故障），分类须与之一致，而不是「其它错误」。
		ctx, cancel := context.WithTimeout(context.Background(), -time.Second)
		defer cancel()
		drainStream(p, ctx)
		rec := sink.recs[0]
		if rec.ErrClass != ClassNet || !strings.Contains(rec.FinishReason, "stream abandoned") {
			t.Fatalf("放弃的流应按放弃的原因分类：%v（%q）", rec.ErrClass, rec.FinishReason)
		}
	})
}

// 审计在最需要的地方不能失明：模型返回残缺的工具参数时，回复与回传历史都含非法 JSON，
// 审计仍须落下原文（非法参数改存为 JSON 字符串），而不是序列化失败落成空值。
func TestInstrumentProvider_AuditsMalformedToolArgs(t *testing.T) {
	const broken = `{"expression": "2+`
	sink := &captureSink{}
	p := InstrumentProvider(&metaStubProvider{Response: Response{
		ToolCalls: []ToolCall{{ID: "call_2", Name: "calculator", Arguments: json.RawMessage(broken)}},
	}}, sink)

	req := Request{Messages: []Message{
		{Role: RoleUser, Content: "计算"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "calculator", Arguments: json.RawMessage(broken)}}},
	}}
	if _, err := p.Complete(context.Background(), req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	rec := sink.recs[0]
	for name, raw := range map[string][]byte{"messages": rec.Messages, "result": rec.Result} {
		if !json.Valid(raw) || !bytes.Contains(raw, []byte(`2+`)) {
			t.Errorf("%s 应保留残缺参数原文，得 %s", name, raw)
		}
	}
}
