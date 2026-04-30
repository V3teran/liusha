package react

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/V3teran/liusha/internal/tool"
	"github.com/V3teran/liusha/internal/llm"
)

// scriptedGen 是按预设脚本回放 Result 的 mock Generator。
type scriptedGen struct {
	turns  []llm.Result
	cursor int
}

func (s *scriptedGen) Provider() string { return "scripted" }
func (s *scriptedGen) Model() string    { return "x" }
func (s *scriptedGen) Generate(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	r := s.turns[s.cursor]
	s.cursor++
	return r, nil
}

// captureAction 是 mock Action：可注入 err 模拟中间件异常。
type captureAction struct {
	name   string
	called int
	res    tool.Result
	err    error
}

func (a *captureAction) Name() string                    { return a.name }
func (a *captureAction) Description() string             { return a.name }
func (a *captureAction) ParametersJSON() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (a *captureAction) Execute(_ context.Context, _ json.RawMessage) (tool.Result, error) {
	a.called++
	if a.err != nil {
		return tool.Result{}, a.err
	}
	return a.res, nil
}

// fakeObserver 用于驱动 Observer 路径测试。
type fakeObserver struct {
	verdicts []Verdict
	calls    int
}

func (f *fakeObserver) Evaluate(ctx context.Context, window []StepRecord) Verdict {
	v := f.verdicts[f.calls%len(f.verdicts)]
	f.calls++
	return v
}

func TestRun_StopsOnDone(t *testing.T) {
	gen := &scriptedGen{turns: []llm.Result{
		{ToolCalls: []llm.ToolCall{{ID: "1", Name: "done", Arguments: json.RawMessage(`{"reason":"ok"}`)}}, FinishReason: "tool_calls"},
	}}
	reg := tool.NewRegistry()
	doneAct := &captureAction{name: "done", res: tool.Result{Done: true}}
	_ = reg.Register(doneAct)

	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg, Budget: Budget{MaxSteps: 5},
		SystemPrompt: "you are a sniffer",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "done" || doneAct.called != 1 {
		t.Fatalf("unexpected: %+v done=%d", out, doneAct.called)
	}
}

func TestRun_StopsOnMaxSteps(t *testing.T) {
	loopCall := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "x", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{loopCall, loopCall, loopCall}}
	reg := tool.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop"})

	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg, Budget: Budget{MaxSteps: 2},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "max_steps" || out.TotalSteps != 2 {
		t.Fatalf("unexpected: %+v", out)
	}
}

func TestRun_ObserverAbortsLowValue(t *testing.T) {
	// 6 步循环，每 5 步触发一次 Observer；第一次返回 abort_low_value，应当 break
	noop := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "n", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{noop, noop, noop, noop, noop, noop, noop}}
	reg := tool.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop"})

	obs := &fakeObserver{verdicts: []Verdict{{Decision: VerdictAbort}}}
	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg,
		Budget:   Budget{MaxSteps: 30},
		Observer: obs, ObserverEverySteps: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "observer_abort" {
		t.Fatalf("expected terminate_by=observer_abort, got %q", out.TerminateBy)
	}
	if obs.calls != 1 {
		t.Fatalf("expected observer 1 call, got %d", obs.calls)
	}
}

func TestRun_ObserverInjectsHint(t *testing.T) {
	// Observer 返回 steer_with_hint，runtime 计数 +1 并继续；最终 done
	noop := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "n", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	terminate := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "d", Name: "done", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{noop, noop, noop, noop, noop, terminate}}
	reg := tool.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop"})
	_ = reg.Register(&captureAction{name: "done", res: tool.Result{Done: true}})

	obs := &fakeObserver{verdicts: []Verdict{{Decision: VerdictSteer, Hint: "改向 X"}}}
	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg,
		Budget:   Budget{MaxSteps: 30},
		Observer: obs, ObserverEverySteps: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "done" {
		t.Fatalf("expected done, got %q", out.TerminateBy)
	}
	if out.ObserverHints != 1 {
		t.Fatalf("expected hints=1, got %d", out.ObserverHints)
	}
}

func TestRun_DoneValidateRejectsThenForce(t *testing.T) {
	// done 前 N 次被 done_validate middleware 拒绝（ErrDoneNotReady），runtime 注入 user msg；
	// 累计达到 doneForceMaxRejects 后强制放行（done_force）。
	doneCall := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "d", Name: "done", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{doneCall, doneCall, doneCall, doneCall}}
	reg := tool.NewRegistry()
	_ = reg.Register(&captureAction{name: "done", err: ErrDoneNotReady{Missing: []string{"replay_multi_identity"}}})

	out, err := Run(context.Background(), Config{LLM: gen, Actions: reg, Budget: Budget{MaxSteps: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "done_force" {
		t.Fatalf("expected done_force, got %q", out.TerminateBy)
	}
	if out.DoneForceCount != 1 {
		t.Fatalf("expected force=1, got %d", out.DoneForceCount)
	}
}

func TestRun_OnAbort(t *testing.T) {
	// OnAbort 返回 (true, nil) 时应当终止，terminate_by="aborted"。
	noop := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "n", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{noop, noop}}
	reg := tool.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop"})

	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg, Budget: Budget{MaxSteps: 5},
		OnAbort: func(_ context.Context) (bool, error) { return true, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "aborted" {
		t.Fatalf("expected aborted, got %q", out.TerminateBy)
	}
	if out.TotalSteps != 0 {
		t.Fatalf("expected 0 steps before abort, got %d", out.TotalSteps)
	}
}
