package react

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/llm"
)

// TestNormalizeDecision_TypoTolerance 验证 LLM 输出 decision 字段的归一化容错。
// 关键 case：实测 LLM 偶发拼写变体不应再触发 unknown warn。
func TestNormalizeDecision_TypoTolerance(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"canonical continue", "continue", VerdictContinue},
		{"canonical redirect", "redirect", VerdictRedirect},
		{"canonical terminate", "terminate", VerdictTerminate},
		{"uppercase CONTINUE", "CONTINUE", VerdictContinue},
		{"with whitespace", "  continue  ", VerdictContinue},
		{"contains keep", "keep going", VerdictContinue},
		{"contains keep substr", "let me keep going", VerdictContinue},
		{"hyphen variant terminate", "abort-now", VerdictTerminate},
		{"contains steer alias", "Please steer", VerdictRedirect},
		{"empty input returns empty", "", ""},
		{"unknown returns empty", "yolo", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := normalizeDecision(tc.raw); got != tc.want {
				t.Fatalf("normalizeDecision(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

// mockGen 是供 reviewer 测试使用的最小 LLM Generator。
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

// stateReaderStub 在 reviewer 路径里替代真 owner store (passive_session/active_scan)，便于注入测试数据。
type stateReaderStub struct {
	out []byte
	err error
}

func (s *stateReaderStub) ReadNotes(_ context.Context, _, _ string) ([]byte, error) {
	return s.out, s.err
}

func TestLLMReviewer_KeepGoingOnInvalidJSON(t *testing.T) {
	gen := &mockGen{out: "not json"}
	r := NewLLMReviewer(gen, nil, "eid", "h")

	v := r.Evaluate(context.Background(), nil)

	if v.Decision != VerdictContinue {
		t.Fatalf("解析失败应回退 continue，实际 %q", v.Decision)
	}
}

func TestLLMReviewer_AbortDecision(t *testing.T) {
	gen := &mockGen{out: `{"decision":"terminate","hint":""}`}
	r := NewLLMReviewer(gen, nil, "eid", "h")

	v := r.Evaluate(context.Background(), []StepRecord{{ActionName: "noop"}})

	if v.Decision != VerdictTerminate {
		t.Fatalf("期望 terminate，实际 %q", v.Decision)
	}
}

func TestLLMReviewer_SteerWithHint(t *testing.T) {
	gen := &mockGen{out: `{"decision":"redirect","hint":"改向 X"}`}
	r := NewLLMReviewer(gen, nil, "eid", "h")

	v := r.Evaluate(context.Background(), []StepRecord{{ActionName: "scan"}})

	if v.Decision != VerdictRedirect {
		t.Fatalf("期望 redirect，实际 %q", v.Decision)
	}
	if v.Hint != "改向 X" {
		t.Fatalf("hint 透传错，实际 %q", v.Hint)
	}
}

func TestLLMReviewer_UnknownDecisionFallsBack(t *testing.T) {
	gen := &mockGen{out: `{"decision":"unknown","hint":""}`}
	r := NewLLMReviewer(gen, nil, "eid", "h")

	v := r.Evaluate(context.Background(), []StepRecord{{ActionName: "noop"}})

	if v.Decision != VerdictContinue {
		t.Fatalf("非法 decision 应回退 continue，实际 %q", v.Decision)
	}
}

func TestLLMReviewer_LLMError(t *testing.T) {
	gen := &mockGen{err: errors.New("network down")}
	r := NewLLMReviewer(gen, nil, "eid", "h")

	v := r.Evaluate(context.Background(), []StepRecord{{ActionName: "noop"}})

	if v.Decision != VerdictContinue {
		t.Fatalf("LLM 错误应回退 continue，实际 %q", v.Decision)
	}
}

func TestLLMReviewer_WindowEmpty(t *testing.T) {
	// window=nil + nil store：仍应正常工作，不 panic
	gen := &mockGen{out: `{"decision":"continue","hint":""}`}
	r := NewLLMReviewer(gen, nil, "eid", "h")

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("空 window 不应 panic，实际 %v", r)
		}
	}()

	v := r.Evaluate(context.Background(), nil)
	if v.Decision != VerdictContinue {
		t.Fatalf("期望 continue，实际 %q", v.Decision)
	}
}

func TestLLMReviewer_StateReadFailureFallsThrough(t *testing.T) {
	// store ReadNotes 返回错误：reviewer 应跳过 state，仍调 LLM
	gen := &mockGen{out: `{"decision":"continue","hint":""}`}
	store := &stateReaderStub{err: errors.New("db down")}
	r := NewLLMReviewer(gen, store, "eid", "h")

	v := r.Evaluate(context.Background(), []StepRecord{{ActionName: "scan"}})

	if v.Decision != VerdictContinue {
		t.Fatalf("期望 continue，实际 %q", v.Decision)
	}
	if len(gen.lastMsgs) == 0 {
		t.Fatalf("store 失败时仍应调 LLM")
	}
}

// TestLLMReviewer_HostFindingsFetcher_Nil：未注入 → prompt 不含 host finding 段
func TestLLMReviewer_HostFindingsFetcher_Nil(t *testing.T) {
	gen := &mockGen{out: `{"decision":"continue","hint":""}`}
	r := NewLLMReviewer(gen, nil, "eid", "h")
	r.Evaluate(context.Background(), []StepRecord{{ActionName: "scan"}})

	user := gen.lastMsgs[1].Content
	if strings.Contains(user, "已有 finding") {
		t.Fatalf("HostFindingsFetcher nil 时不应注入 host finding 段，prompt: %s", user)
	}
	// 反例：确保旧"已挖 finding 数"段彻底消失（防止回归）
	if strings.Contains(user, "已挖 finding 数") {
		t.Fatalf("旧'已挖 finding 数'段不应再出现（D 方案已删除），prompt: %s", user)
	}
}

// TestLLMReviewer_HostFindingsFetcher_RendersListOnly：渲染为列表，不带 count 等暗示进度的措辞
func TestLLMReviewer_HostFindingsFetcher_RendersListOnly(t *testing.T) {
	gen := &mockGen{out: `{"decision":"continue","hint":""}`}
	r := NewLLMReviewer(gen, nil, "eid", "h")
	r.HostFindingsFetcher = func(_ context.Context) ([]string, error) {
		return []string{
			"[critical] IDOR in /api/bac/order/7",
			"[high] Vertical priv esc in /api/bac/admin/users",
		}, nil
	}
	r.Evaluate(context.Background(), []StepRecord{{ActionName: "scan"}})

	user := gen.lastMsgs[1].Content
	if !strings.Contains(user, "已有 finding") || !strings.Contains(user, "不**作 terminate 信号") {
		t.Fatalf("应渲染'背景参考'段头，prompt: %s", user)
	}
	if !strings.Contains(user, "[critical] IDOR") || !strings.Contains(user, "[high] Vertical priv esc") {
		t.Fatalf("应含列表项，prompt: %s", user)
	}
	// 关键反例：D 方案核心约束——不能出现"已挖 finding 数"这种带 count 的措辞
	if strings.Contains(user, "已挖 finding 数") {
		t.Fatalf("HostFindings 段不应含'已挖 finding 数'(回归 bac/profile 误推 done 的 bug)，prompt: %s", user)
	}
}

// TestLLMReviewer_HostFindingsFetcher_EmptyListNoSection：返回空列表 → 不渲染段头
func TestLLMReviewer_HostFindingsFetcher_EmptyListNoSection(t *testing.T) {
	gen := &mockGen{out: `{"decision":"continue","hint":""}`}
	r := NewLLMReviewer(gen, nil, "eid", "h")
	r.HostFindingsFetcher = func(_ context.Context) ([]string, error) {
		return nil, nil
	}
	r.Evaluate(context.Background(), []StepRecord{{ActionName: "scan"}})

	user := gen.lastMsgs[1].Content
	if strings.Contains(user, "已有 finding") {
		t.Fatalf("空列表不应渲染段头，prompt: %s", user)
	}
}

// TestLLMReviewer_FullObs_UsedForLastStep：末尾 1 步 FullObs 非空 → 用 FullObs 而非 ObsSummary
func TestLLMReviewer_FullObs_UsedForLastStep(t *testing.T) {
	gen := &mockGen{out: `{"decision":"continue","hint":""}`}
	r := NewLLMReviewer(gen, nil, "eid", "h")
	window := []StepRecord{
		{StepIdx: 1, ActionName: "run_command", Args: []byte(`{"x":1}`), ObsSummary: "old summary 1"},
		{StepIdx: 2, ActionName: "run_command", Args: []byte(`{"x":2}`), ObsSummary: "TRUNCATED", FullObs: "RAW: SUCCESS: admin:password"},
	}
	r.Evaluate(context.Background(), window)

	user := gen.lastMsgs[1].Content
	if !strings.Contains(user, "SUCCESS: admin:password") {
		t.Fatalf("末尾步应用 FullObs，prompt: %s", user)
	}
	if !strings.Contains(user, "old summary 1") {
		t.Fatalf("非末尾步应保留 ObsSummary，prompt: %s", user)
	}
	if strings.Contains(user, "TRUNCATED") {
		t.Fatalf("末尾步的 ObsSummary 不应再出现（被 FullObs 替代），prompt: %s", user)
	}
}
