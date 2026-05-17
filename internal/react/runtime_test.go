package react

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/llm"
	"github.com/V3teran/liusha/internal/toolruntime"
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

// captureAction 是 mock Action：可注入 err 模拟 Interceptor 异常。
type captureAction struct {
	name   string
	called int
	res    toolfx.Result
	err    error
}

func (a *captureAction) Name() string                    { return a.name }
func (a *captureAction) Description() string             { return a.name }
func (a *captureAction) ParametersJSON() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (a *captureAction) Execute(_ context.Context, _ json.RawMessage) (toolfx.Result, error) {
	a.called++
	if a.err != nil {
		return toolfx.Result{}, a.err
	}
	return a.res, nil
}

// fakeReviewer 用于驱动 Reviewer 路径测试。
type fakeReviewer struct {
	verdicts []Verdict
	calls    int
}

func (f *fakeReviewer) Evaluate(ctx context.Context, window []StepRecord) Verdict {
	v := f.verdicts[f.calls%len(f.verdicts)]
	f.calls++
	return v
}

func TestRun_StopsOnDone(t *testing.T) {
	gen := &scriptedGen{turns: []llm.Result{
		{ToolCalls: []llm.ToolCall{{ID: "1", Name: "done", Arguments: json.RawMessage(`{"reason":"ok"}`)}}, FinishReason: "tool_calls"},
	}}
	reg := toolfx.NewRegistry()
	doneAct := &captureAction{name: "done", res: toolfx.Result{Done: true}}
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
	reg := toolfx.NewRegistry()
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

func TestRun_ReviewerTerminateInjectsHint(t *testing.T) {
	// reviewer terminate 不再 break 主循环——改注入强 hint，让 LLM 自决 done()。
	// 场景：跑 5 步 noop，第 6 步前 reviewer 触发 terminate → 注入 hint → LLM 看到后下一步调 done。
	noop := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "n", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	doneCall := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "d", Name: "done", Arguments: json.RawMessage(`{"reason":"reviewer hinted"}`)}},
		FinishReason: "tool_calls",
	}
	// 5 noop + 1 done（第 6 turn LLM 看到 reviewer hint 后乖乖 done）
	gen := &scriptedGen{turns: []llm.Result{noop, noop, noop, noop, noop, doneCall}}
	reg := toolfx.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop"})
	_ = reg.Register(&captureAction{name: "done", res: toolfx.Result{Done: true}})

	r := &fakeReviewer{verdicts: []Verdict{{Decision: VerdictTerminate, Hint: "done now"}}}
	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg,
		Budget:   Budget{MaxSteps: 30},
		Reviewer: r, ReviewerEverySteps: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy == "reviewer_terminate" {
		t.Fatalf("reviewer terminate 应注入 hint 不应 break；实际 terminate_by=%q", out.TerminateBy)
	}
	if out.ReviewerHints == 0 {
		t.Fatalf("expected ReviewerHints>0（terminate 注入 hint），实际 %d", out.ReviewerHints)
	}
}

func TestRun_ReviewerInjectsHint(t *testing.T) {
	// Reviewer 返回 redirect，runtime 计数 +1 并继续；最终 done
	noop := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "n", Name: "noop", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	terminate := llm.Result{
		ToolCalls:    []llm.ToolCall{{ID: "d", Name: "done", Arguments: json.RawMessage(`{}`)}},
		FinishReason: "tool_calls",
	}
	gen := &scriptedGen{turns: []llm.Result{noop, noop, noop, noop, noop, terminate}}
	reg := toolfx.NewRegistry()
	_ = reg.Register(&captureAction{name: "noop"})
	_ = reg.Register(&captureAction{name: "done", res: toolfx.Result{Done: true}})

	r := &fakeReviewer{verdicts: []Verdict{{Decision: VerdictRedirect, Hint: "改向 X"}}}
	out, err := Run(context.Background(), Config{
		LLM: gen, Actions: reg,
		Budget:   Budget{MaxSteps: 30},
		Reviewer: r, ReviewerEverySteps: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.TerminateBy != "done" {
		t.Fatalf("expected done, got %q", out.TerminateBy)
	}
	if out.ReviewerHints != 1 {
		t.Fatalf("expected hints=1, got %d", out.ReviewerHints)
	}
}

// fnAction 是测试用可注入函数 action：让单个 tool_call 调度可观测（如并发计数）。
type fnAction struct {
	name string
	fn   func(ctx context.Context, args json.RawMessage) (toolfx.Result, error)
}

func (a *fnAction) Name() string                    { return a.name }
func (a *fnAction) Description() string             { return "" }
func (a *fnAction) ParametersJSON() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }
func (a *fnAction) Execute(ctx context.Context, args json.RawMessage) (toolfx.Result, error) {
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
	reg := toolfx.NewRegistry()

	slow := &fnAction{
		name: "slow_tool",
		fn: func(_ context.Context, _ json.RawMessage) (toolfx.Result, error) {
			n := current.Add(1)
			for {
				cur := concurrentMax.Load()
				if n <= cur || concurrentMax.CompareAndSwap(cur, n) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
			current.Add(-1)
			return toolfx.Result{Summary: "ok"}, nil
		},
	}
	if err := reg.Register(slow); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(&captureAction{name: "done", res: toolfx.Result{Done: true}}); err != nil {
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
	reg := toolfx.NewRegistry()
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

// TestCompressImages_KeepsRecentN 验证倒序保留最近 N 张图，更早的换占位文本。
// 8 条 message 各 1 张图，maxImages=5 时：最后 5 张原样保留，前 3 张换 placeholder。
func TestCompressImages_KeepsRecentN(t *testing.T) {
	mkMsg := func(idx int) llm.Message {
		return llm.Message{
			Role: llm.RoleTool,
			ContentParts: []llm.ContentPart{
				{Type: "text", Text: "obs-" + string(rune('0'+idx))},
				{Type: "image_url", ImageURL: &llm.ImageContent{
					MediaType: "image/png", Base64Data: "data-" + string(rune('0'+idx)),
				}},
			},
		}
	}
	msgs := make([]llm.Message, 8)
	for i := range msgs {
		msgs[i] = mkMsg(i)
	}

	compressImages(msgs, 5)

	var realImages, placeholders int
	for _, m := range msgs {
		for _, p := range m.ContentParts {
			if p.Type == "image_url" && p.ImageURL != nil && p.ImageURL.Base64Data != "" {
				realImages++
			}
			if p.Type == "text" && p.Text == imageRemovedPlaceholder {
				placeholders++
			}
		}
	}
	if realImages != 5 {
		t.Errorf("应保留 5 张真图，实际 %d", realImages)
	}
	if placeholders != 3 {
		t.Errorf("应有 3 个占位，实际 %d", placeholders)
	}
	// 倒序保留：最后 5 条 (idx 3-7) 应为真图，前 3 条 (idx 0-2) 应为占位
	for i := 0; i < 3; i++ {
		if msgs[i].ContentParts[1].Type != "text" {
			t.Errorf("msgs[%d] 应为占位 type=text，实际 %s", i, msgs[i].ContentParts[1].Type)
		}
	}
	for i := 3; i < 8; i++ {
		if msgs[i].ContentParts[1].Type != "image_url" {
			t.Errorf("msgs[%d] 应保留 image_url，实际 %s", i, msgs[i].ContentParts[1].Type)
		}
	}
}

// TestCompressImages_NoOp 验证图数 ≤ max 时不动 + maxImages=0 时跳过。
func TestCompressImages_NoOp(t *testing.T) {
	mkImg := func() llm.Message {
		return llm.Message{Role: llm.RoleTool, ContentParts: []llm.ContentPart{
			{Type: "image_url", ImageURL: &llm.ImageContent{MediaType: "image/png", Base64Data: "x"}},
		}}
	}
	// 3 张 + max=5 → 全保留
	msgs := []llm.Message{mkImg(), mkImg(), mkImg()}
	compressImages(msgs, 5)
	for i, m := range msgs {
		if m.ContentParts[0].Type != "image_url" {
			t.Errorf("msgs[%d] 不应被改，实际 type=%s", i, m.ContentParts[0].Type)
		}
	}
	// maxImages=0 → 早返
	msgs2 := []llm.Message{mkImg()}
	compressImages(msgs2, 0)
	if msgs2[0].ContentParts[0].Type != "image_url" {
		t.Errorf("maxImages=0 不应动 msg，实际 type=%s", msgs2[0].ContentParts[0].Type)
	}
}
