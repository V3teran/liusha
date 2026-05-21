package llm

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/llminvocation"
)

// TestSanitizeToolCalls_ValidPassThrough 合法 JSON 透传不变。
func TestSanitizeToolCalls_ValidPassThrough(t *testing.T) {
	in := []ToolCall{{ID: "1", Name: "x", Arguments: json.RawMessage(`{"k":"v"}`)}}
	out := sanitizeToolCalls(in)
	if string(out[0].Arguments) != `{"k":"v"}` {
		t.Fatalf("合法 RawMessage 应原样透传，got %s", out[0].Arguments)
	}
}

// TestSanitizeToolCalls_InvalidWrapped 非法 JSON 应被包装成 {"_raw_invalid":"..."}。
// 真实场景：DeepSeek 返回截断的 ToolCall.Arguments 字符串。
func TestSanitizeToolCalls_InvalidWrapped(t *testing.T) {
	bad := json.RawMessage(`{"flow_id":1,abc`)
	in := []ToolCall{{ID: "1", Name: "x", Arguments: bad}}
	out := sanitizeToolCalls(in)
	if !json.Valid(out[0].Arguments) {
		t.Fatalf("sanitize 后必须是合法 JSON，got %s", out[0].Arguments)
	}
	if !strings.Contains(string(out[0].Arguments), `"_raw_invalid"`) {
		t.Fatalf("应含 _raw_invalid 标记保留原字节，got %s", out[0].Arguments)
	}
	// 业务路径：原 in 切片不变（sanitize 用浅拷贝）
	if string(in[0].Arguments) != `{"flow_id":1,abc` {
		t.Fatalf("原 in[0].Arguments 不应被修改")
	}
}

// TestSanitizeResult_MarshalSucceedsAfterSanitize 端到端：先前会让 json.Marshal(res)
// 整个失败的非法 RawMessage，sanitize 后应能正确序列化。
func TestSanitizeResult_MarshalSucceedsAfterSanitize(t *testing.T) {
	res := Result{
		Content: "hi",
		ToolCalls: []ToolCall{
			{ID: "good", Name: "ok", Arguments: json.RawMessage(`{"a":1}`)},
			{ID: "bad", Name: "ok", Arguments: json.RawMessage(`not-json`)},
		},
	}
	if _, err := json.Marshal(res); err == nil {
		t.Fatal("setup 不对：原 res 应 marshal 失败才有意义")
	}
	if _, err := json.Marshal(sanitizeResult(res)); err != nil {
		t.Fatalf("sanitize 后必须 marshal 成功，got %v", err)
	}
}

// stubGen 用于测试：可注入返回结果、错误和延迟。
type stubGen struct {
	res   Result
	err   error
	sleep time.Duration

	provider string
	model    string
}

func (f *stubGen) Generate(_ context.Context, _ []Message, _ []ToolSchema) (Result, error) {
	if f.sleep > 0 {
		time.Sleep(f.sleep)
	}
	return f.res, f.err
}
func (f *stubGen) Provider() string { return f.provider }
func (f *stubGen) Model() string    { return f.model }

// fakeSink 收集 Append 的调用，测试用。
type fakeSink struct {
	calls     []llminvocation.Invocation
	appendErr error
}

func (s *fakeSink) Append(_ context.Context, c llminvocation.Invocation) (int64, error) {
	s.calls = append(s.calls, c)
	if s.appendErr != nil {
		return 0, s.appendErr
	}
	return int64(len(s.calls)), nil
}

// fixedPricing 返回固定 cost，便于断言。
type fixedPricing struct{ cost float64 }

func (p fixedPricing) Estimate(_, _ string, _ Usage) float64 { return p.cost }

func TestInstrument_AppendsCallOnSuccess(t *testing.T) {
	t.Parallel()
	inner := &stubGen{
		provider: "deepseek",
		model:    "deepseek-chat",
		res: Result{
			Content:      "hi",
			Usage:        Usage{InTokens: 100, OutTokens: 30, CachedTokens: 10},
			FinishReason: "stop",
			Provider:     "deepseek",
			Model:        "deepseek-chat",
		},
	}
	sink := &fakeSink{}
	tid, oid := "task-1", "owner-1"
	ot := "passive_session"
	g := Instrument(inner, sink, CallMeta{
		TaskID:    &tid,
		OwnerType: &ot,
		OwnerID:   &oid,
		RouteKey:  "tracker",
	}, fixedPricing{cost: 0.0042})

	res, err := g.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("Generate returned err: %v", err)
	}
	if res.Content != "hi" {
		t.Fatalf("expected res.Content=hi, got %q", res.Content)
	}
	if len(sink.calls) != 1 {
		t.Fatalf("expected 1 sink.Append call, got %d", len(sink.calls))
	}
	c := sink.calls[0]
	if c.Provider != "deepseek" || c.Model != "deepseek-chat" {
		t.Fatalf("provider/model mismatch: %+v", c)
	}
	if c.InTokens != 100 || c.OutTokens != 30 || c.CachedTokens != 10 {
		t.Fatalf("usage mismatch: %+v", c)
	}
	if c.CostUSD != 0.0042 {
		t.Fatalf("cost mismatch: %v", c.CostUSD)
	}
	if c.FinishReason != "stop" {
		t.Fatalf("finish reason: %q", c.FinishReason)
	}
	if c.Error != "" {
		t.Fatalf("expected no error, got %q", c.Error)
	}
	if c.TaskID == nil || *c.TaskID != "task-1" {
		t.Fatalf("task id: %v", c.TaskID)
	}
	if c.OwnerID == nil || *c.OwnerID != "owner-1" {
		t.Fatalf("owner id: %v", c.OwnerID)
	}
	if c.Role != "tracker" {
		t.Fatalf("expected role=tracker, got %q", c.Role)
	}
}

func TestInstrument_AppendsCallOnError(t *testing.T) {
	t.Parallel()
	inner := &stubGen{
		provider: "deepseek",
		model:    "deepseek-chat",
		err:      errors.New("boom"),
	}
	sink := &fakeSink{}
	g := Instrument(inner, sink, CallMeta{RouteKey: "inspector"}, fixedPricing{cost: 0.001})

	_, err := g.Generate(context.Background(), nil, nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if len(sink.calls) != 1 {
		t.Fatalf("expected 1 sink call, got %d", len(sink.calls))
	}
	c := sink.calls[0]
	if c.Error != "boom" {
		t.Fatalf("expected Error=boom, got %q", c.Error)
	}
	// 失败路径不估算 cost（usage 不可信）。
	if c.CostUSD != 0 {
		t.Fatalf("expected zero cost on error, got %v", c.CostUSD)
	}
	if c.Role != "inspector" {
		t.Fatalf("expected role=inspector, got %q", c.Role)
	}
}

// recordingPricing 验证 Estimate 被传入正确参数 + 返回值落入 cost。
type recordingPricing struct {
	gotProvider string
	gotModel    string
	gotUsage    Usage
	ret         float64
}

func (p *recordingPricing) Estimate(provider, model string, u Usage) float64 {
	p.gotProvider = provider
	p.gotModel = model
	p.gotUsage = u
	return p.ret
}

func TestInstrument_CostCalculatedFromPricing(t *testing.T) {
	t.Parallel()
	inner := &stubGen{
		provider: "deepseek",
		model:    "deepseek-chat",
		res: Result{
			Usage:    Usage{InTokens: 100, OutTokens: 30, CachedTokens: 10},
			Provider: "deepseek",
			Model:    "deepseek-chat",
		},
	}
	sink := &fakeSink{}
	pr := &recordingPricing{ret: 0.0123}
	g := Instrument(inner, sink, CallMeta{RouteKey: "tracker"}, pr)
	if _, err := g.Generate(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	if pr.gotProvider != "deepseek" || pr.gotModel != "deepseek-chat" {
		t.Fatalf("pricing not invoked with provider/model: %+v", pr)
	}
	if pr.gotUsage != (Usage{InTokens: 100, OutTokens: 30, CachedTokens: 10}) {
		t.Fatalf("pricing usage mismatch: %+v", pr.gotUsage)
	}
	if got := sink.calls[0].CostUSD; got != 0.0123 {
		t.Fatalf("cost mismatch: got %v want 0.0123", got)
	}
}

func TestInstrument_LatencyMeasured(t *testing.T) {
	t.Parallel()
	inner := &stubGen{
		provider: "deepseek",
		model:    "deepseek-chat",
		sleep:    100 * time.Millisecond,
		res: Result{
			Usage:    Usage{InTokens: 1, OutTokens: 1},
			Provider: "deepseek",
			Model:    "deepseek-chat",
		},
	}
	sink := &fakeSink{}
	g := Instrument(inner, sink, CallMeta{RouteKey: "tracker"}, fixedPricing{cost: 0})
	if _, err := g.Generate(context.Background(), nil, nil); err != nil {
		t.Fatal(err)
	}
	got := sink.calls[0].LatencyMs
	if got < 90 || got > 500 {
		t.Fatalf("latency out of expected range [90,500]ms: %d", got)
	}
}

func TestInstrument_RouteKeyWritten(t *testing.T) {
	t.Parallel()
	cases := []string{"tracker", "commander", "striker", "inspector"}
	for _, rk := range cases {
		rk := rk
		t.Run(rk, func(t *testing.T) {
			t.Parallel()
			inner := &stubGen{
				provider: "deepseek",
				model:    "deepseek-chat",
				res:      Result{Provider: "deepseek", Model: "deepseek-chat"},
			}
			sink := &fakeSink{}
			g := Instrument(inner, sink, CallMeta{RouteKey: rk}, fixedPricing{cost: 0})
			if _, err := g.Generate(context.Background(), nil, nil); err != nil {
				t.Fatal(err)
			}
			if got := sink.calls[0].Role; got != rk {
				t.Fatalf("expected role=%q, got %q", rk, got)
			}
		})
	}
}

func TestInstrument_SinkErrorDoesNotBlockGenerate(t *testing.T) {
	t.Parallel()
	inner := &stubGen{
		provider: "deepseek",
		model:    "deepseek-chat",
		res: Result{
			Content:  "ok",
			Provider: "deepseek",
			Model:    "deepseek-chat",
		},
	}
	sink := &fakeSink{appendErr: errors.New("db down")}
	g := Instrument(inner, sink, CallMeta{RouteKey: "tracker"}, fixedPricing{cost: 0})

	res, err := g.Generate(context.Background(), nil, nil)
	if err != nil {
		t.Fatalf("expected sink err to be swallowed, got %v", err)
	}
	if res.Content != "ok" {
		t.Fatalf("inner result lost: %q", res.Content)
	}
}

func TestInstrument_PassThroughProviderModel(t *testing.T) {
	t.Parallel()
	inner := &stubGen{provider: "anthropic", model: "claude-sonnet-4-6"}
	sink := &fakeSink{}
	g := Instrument(inner, sink, CallMeta{}, fixedPricing{cost: 0})
	if g.Provider() != "anthropic" {
		t.Fatalf("expected Provider=anthropic, got %q", g.Provider())
	}
	if g.Model() != "claude-sonnet-4-6" {
		t.Fatalf("expected Model=claude-sonnet-4-6, got %q", g.Model())
	}
}
