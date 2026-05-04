package react

import (
	"context"
	"errors"
	"testing"

	"github.com/V3teran/liusha/internal/llm"
)

// mockGen 是供 observer / lesson_extract 测试使用的最小 LLM Generator。
//
//   - out：Generate 直接把 out 包成 llm.Result.Content 返回；
//   - err：非 nil 时 Generate 返回该错误（覆盖网络失败路径）；
//   - lastMsgs：捕获最近一次 Generate 的消息，便于断言 prompt 拼装。
type mockGen struct {
	out      string
	err      error
	lastMsgs []llm.Message
}

func (m *mockGen) Provider() string { return "mock" }
func (m *mockGen) Model() string    { return "mock" }
func (m *mockGen) Generate(_ context.Context, msgs []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	m.lastMsgs = msgs
	if m.err != nil {
		return llm.Result{}, m.err
	}
	return llm.Result{Content: m.out}, nil
}

// stateReaderStub 在 observer 路径里替代真 *engagement.Store，便于注入测试数据。
type stateReaderStub struct {
	out []byte
	err error
}

func (s *stateReaderStub) ReadState(_ context.Context, _ string) ([]byte, error) {
	return s.out, s.err
}

func TestLLMObserver_KeepGoingOnInvalidJSON(t *testing.T) {
	gen := &mockGen{out: "not json"}
	obs := NewLLMObserver(gen, nil, "eid")

	v := obs.Evaluate(context.Background(), nil)

	if v.Decision != VerdictKeepGoing {
		t.Fatalf("解析失败应回退 keep_going，实际 %q", v.Decision)
	}
}

func TestLLMObserver_AbortDecision(t *testing.T) {
	gen := &mockGen{out: `{"decision":"abort_low_value","hint":""}`}
	obs := NewLLMObserver(gen, nil, "eid")

	v := obs.Evaluate(context.Background(), []StepRecord{{ActionName: "noop"}})

	if v.Decision != VerdictAbort {
		t.Fatalf("期望 abort_low_value，实际 %q", v.Decision)
	}
}

func TestLLMObserver_SteerWithHint(t *testing.T) {
	gen := &mockGen{out: `{"decision":"steer_with_hint","hint":"改向 X"}`}
	obs := NewLLMObserver(gen, nil, "eid")

	v := obs.Evaluate(context.Background(), []StepRecord{{ActionName: "scan"}})

	if v.Decision != VerdictSteer {
		t.Fatalf("期望 steer_with_hint，实际 %q", v.Decision)
	}
	if v.Hint != "改向 X" {
		t.Fatalf("hint 透传错，实际 %q", v.Hint)
	}
}

func TestLLMObserver_UnknownDecisionFallsBack(t *testing.T) {
	gen := &mockGen{out: `{"decision":"unknown","hint":""}`}
	obs := NewLLMObserver(gen, nil, "eid")

	v := obs.Evaluate(context.Background(), []StepRecord{{ActionName: "noop"}})

	if v.Decision != VerdictKeepGoing {
		t.Fatalf("非法 decision 应回退 keep_going，实际 %q", v.Decision)
	}
}

func TestLLMObserver_LLMError(t *testing.T) {
	gen := &mockGen{err: errors.New("network down")}
	obs := NewLLMObserver(gen, nil, "eid")

	v := obs.Evaluate(context.Background(), []StepRecord{{ActionName: "noop"}})

	if v.Decision != VerdictKeepGoing {
		t.Fatalf("LLM 错误应回退 keep_going，实际 %q", v.Decision)
	}
}

func TestLLMObserver_WindowEmpty(t *testing.T) {
	// window=nil + nil store：仍应正常工作，不 panic
	gen := &mockGen{out: `{"decision":"keep_going","hint":""}`}
	obs := NewLLMObserver(gen, nil, "eid")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("空 window 不应 panic，实际 %v", r)
		}
	}()

	v := obs.Evaluate(context.Background(), nil)
	if v.Decision != VerdictKeepGoing {
		t.Fatalf("期望 keep_going，实际 %q", v.Decision)
	}
}

func TestLLMObserver_StateReadFailureFallsThrough(t *testing.T) {
	// store ReadState 返回错误：observer 应跳过 state，仍调 LLM
	gen := &mockGen{out: `{"decision":"keep_going","hint":""}`}
	store := &stateReaderStub{err: errors.New("db down")}
	obs := NewLLMObserver(gen, store, "eid")

	v := obs.Evaluate(context.Background(), []StepRecord{{ActionName: "scan"}})

	if v.Decision != VerdictKeepGoing {
		t.Fatalf("期望 keep_going，实际 %q", v.Decision)
	}
	if len(gen.lastMsgs) == 0 {
		t.Fatalf("store 失败时仍应调 LLM")
	}
}
