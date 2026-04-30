package react

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

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

// fnAction 是测试用可注入函数 action：让单个 tool_call 调度可观测（如并发计数）。
type fnAction struct {
	name string
	fn   func(ctx context.Context, args json.RawMessage) (tool.Result, error)
}

func (a *fnAction) Name() string                    { return a.name }
func (a *fnAction) Description() string             { return "" }
func (a *fnAction) ParametersJSON() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (a *fnAction) Execute(ctx context.Context, args json.RawMessage) (tool.Result, error) {
	return a.fn(ctx, args)
}

func TestRun_ParallelToolCalls(t *testing.T) {
	// 主 LLM 一轮返 3 个 tool_calls，runtime 应并行执行（concurrentMax ≥ 2）。
	var concurrentMax atomic.Int32
	var current atomic.Int32

	multi := llm.Result{
		ToolCalls: []llm.ToolCall{
			{ID: "1", Name: "slow_tool", Arguments: json.RawMessage(`{}`)},
			{ID: "2", Name: "slow_tool", Arguments: json.RawMessage(`{}`)},
			{ID: "3", Name: "slow_tool", Arguments: json.RawMessage(`{}`)},
		},
		FinishReason: "tool_calls",
	}
	done := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "d", Name: "done", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}

	gen := &scriptedGen{turns: []llm.Result{multi, done}}
	reg := tool.NewRegistry()

	slow := &fnAction{
		name: "slow_tool",
		fn: func(_ context.Context, _ json.RawMessage) (tool.Result, error) {
			n := current.Add(1)
			for {
				cur := concurrentMax.Load()
				if n <= cur || concurrentMax.CompareAndSwap(cur, n) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			current.Add(-1)
			return tool.Result{Summary: "ok"}, nil
		},
	}
	if err := reg.Register(slow); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&captureAction{name: "done", res: tool.Result{Done: true}}); err != nil {
		t.Fatal(err)
	}

	out, err := Run(context.Background(), Config{LLM: gen, Actions: reg, Budget: Budget{MaxSteps: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "done" {
		t.Fatalf("terminate=%q want done", out.TerminateBy)
	}
	if got := concurrentMax.Load(); got < 2 {
		t.Fatalf("concurrentMax=%d want >=2 (parallel exec)", got)
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
