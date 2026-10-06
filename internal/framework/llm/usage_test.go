package llm

import (
	"context"
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
	ch := make(chan StreamEvent)
	close(ch)
	return ch, nil
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
