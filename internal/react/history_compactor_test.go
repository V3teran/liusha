package react

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/V3teran/liusha/internal/llm"
)

// ---- stub Generator -------------------------------------------------------

type stubGen struct {
	content string
	err     error
	calls   int
}

func (g *stubGen) Provider() string { return "stub" }
func (g *stubGen) Model() string    { return "stub-model" }
func (g *stubGen) Generate(_ context.Context, _ []llm.Message, _ []llm.ToolSchema) (llm.Result, error) {
	g.calls++
	if g.err != nil {
		return llm.Result{}, g.err
	}
	return llm.Result{Content: g.content}, nil
}

// ---- estimateMessageTokens ------------------------------------------------

func TestEstimateMessageTokens_TextContent(t *testing.T) {
	m := llm.Message{Content: strings.Repeat("a", 400)} // 100 tokens
	if got := estimateMessageTokens(m); got != 100 {
		t.Fatalf("want 100, got %d", got)
	}
}

func TestEstimateMessageTokens_ImagePart(t *testing.T) {
	m := llm.Message{
		ContentParts: []llm.ContentPart{
			{Type: "text", Text: strings.Repeat("a", 100)}, // 25
			{Type: "image_url", ImageURL: &llm.ImageContent{
				MediaType:  "image/png",
				Base64Data: strings.Repeat("X", 4000), // 1000
			}},
		},
	}
	if got := estimateMessageTokens(m); got != 1025 {
		t.Fatalf("want 1025, got %d", got)
	}
}

// ---- dedupReadToolResults -------------------------------------------------

func TestDedupReadToolResults_KeepLatest(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "user"},
		{Role: llm.RoleTool, Name: "read_findings", Content: "result A"},
		{Role: llm.RoleTool, Name: "browser_use", Content: "dom 1"}, // 不去重（非 dedupTools）
		{Role: llm.RoleTool, Name: "read_findings", Content: "result B"},
		{Role: llm.RoleTool, Name: "read_lessons", Content: "lessons A"},
		{Role: llm.RoleTool, Name: "read_findings", Content: "result C"}, // 最新，保留
	}
	dedupReadToolResults(msgs)

	// 最新 read_findings 保留
	if msgs[6].Content != "result C" {
		t.Errorf("latest read_findings should keep: got %q", msgs[6].Content)
	}
	// 老的 read_findings 折叠
	for _, idx := range []int{2, 4} {
		if !strings.Contains(msgs[idx].Content, "已折叠") {
			t.Errorf("msgs[%d] should be folded: got %q", idx, msgs[idx].Content)
		}
	}
	// browser_use 不去重（非 dedupTools 集合）
	if msgs[3].Content != "dom 1" {
		t.Errorf("browser_use should not dedup: got %q", msgs[3].Content)
	}
	// read_lessons 只有一次，保留
	if msgs[5].Content != "lessons A" {
		t.Errorf("single read_lessons should keep: got %q", msgs[5].Content)
	}
}

// ---- findFirstUserMsgIndex ------------------------------------------------

func TestFindFirstUserMsgIndex_HappyPath(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "first user"},
		{Role: llm.RoleAssistant, Content: "asst"},
		{Role: llm.RoleUser, Content: "inspector hint"}, // 不该被选中
	}
	if got := findFirstUserMsgIndex(msgs); got != 1 {
		t.Fatalf("want 1, got %d", got)
	}
}

func TestFindFirstUserMsgIndex_NoSystem(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "first user"},
		{Role: llm.RoleAssistant, Content: "asst"},
	}
	if got := findFirstUserMsgIndex(msgs); got != 0 {
		t.Fatalf("want 0, got %d", got)
	}
}

// ---- trailingByTokenBudget ------------------------------------------------

func TestTrailingByTokenBudget_BoundaryAlignment(t *testing.T) {
	mkTurn := func(call, result string) []llm.Message {
		return []llm.Message{
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: call}}, Content: strings.Repeat("x", 400)},
			{Role: llm.RoleTool, Name: call, Content: result + strings.Repeat("y", 400)},
		}
	}
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "user"},
	}
	for i := 0; i < 5; i++ {
		msgs = append(msgs, mkTurn("browser_use", "r")...)
	}
	keepFrom := trailingByTokenBudget(msgs, 1, 400)
	if keepFrom < 1 || keepFrom > len(msgs) {
		t.Fatalf("keepFrom out of range: %d", keepFrom)
	}
	if msgs[keepFrom].Role != llm.RoleAssistant || len(msgs[keepFrom].ToolCalls) == 0 {
		t.Errorf("keepFrom should align to turn boundary, got role=%s tool_calls=%d", msgs[keepFrom].Role, len(msgs[keepFrom].ToolCalls))
	}
}

// ---- headTruncateByBudget -------------------------------------------------

func TestHeadTruncateByBudget_PreservesSystemAndFirstUser(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("s", 400)},
		{Role: llm.RoleUser, Content: strings.Repeat("u", 400)},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "browser_use"}}, Content: strings.Repeat("a", 4000)},
		{Role: llm.RoleTool, Name: "browser_use", Content: strings.Repeat("r", 4000)},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "2", Name: "browser_use"}}, Content: strings.Repeat("b", 400)},
		{Role: llm.RoleTool, Name: "browser_use", Content: strings.Repeat("c", 400)},
	}
	out := headTruncateByBudget(msgs, 300)
	if out[0].Role != llm.RoleSystem {
		t.Errorf("first msg should be system")
	}
	if out[1].Role != llm.RoleUser || !strings.HasPrefix(out[1].Content, "uu") {
		t.Errorf("second msg should be first user prompt")
	}
	lastIdx := len(out) - 1
	if out[lastIdx].Role != llm.RoleTool || out[lastIdx].Name != "browser_use" {
		t.Errorf("last msg should be tool result, got role=%s name=%s", out[lastIdx].Role, out[lastIdx].Name)
	}
}

// ---- compactHistory integration ------------------------------------------

func TestCompactHistory_BelowThreshold_NoOp(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: "sys"},
		{Role: llm.RoleUser, Content: "user"},
	}
	cfg := HistoryCompactConfig{TriggerRatio: 0.75, TrailingBudgetRatio: 0.50, CooldownTokenDelta: 4000}
	st := &compactState{}
	gen := &stubGen{content: "should not be called"}
	out := compactHistory(context.Background(), msgs, NewLLMHistoryCompactor(gen), 100000, cfg, st)
	if len(out) != 2 {
		t.Errorf("below threshold should noop, got len=%d", len(out))
	}
	if gen.calls != 0 {
		t.Errorf("should not call LLM, got %d calls", gen.calls)
	}
}

func TestCompactHistory_TriggerThenSummarize(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("s", 4000)},
		{Role: llm.RoleUser, Content: strings.Repeat("u", 4000)},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "browser_use"}}, Content: strings.Repeat("a", 4000)},
			llm.Message{Role: llm.RoleTool, Name: "browser_use", Content: strings.Repeat("r", 4000)},
		)
	}
	cfg := HistoryCompactConfig{TriggerRatio: 0.75, TrailingBudgetRatio: 0.30, CooldownTokenDelta: 1000}
	st := &compactState{}
	gen := &stubGen{content: "压缩后摘要"}

	out := compactHistory(context.Background(), msgs, NewLLMHistoryCompactor(gen), 20000, cfg, st)

	if gen.calls != 1 {
		t.Errorf("expected 1 LLM call, got %d", gen.calls)
	}
	if len(out) >= len(msgs) {
		t.Errorf("output should be shorter than input, got %d >= %d", len(out), len(msgs))
	}
	foundSummary := false
	for _, m := range out {
		if strings.Contains(m.Content, "<历史片段摘要") {
			foundSummary = true
			break
		}
	}
	if !foundSummary {
		t.Errorf("summary msg with tag not found in output")
	}
}

func TestCompactHistory_CompactorFailure_FallbackToTruncate(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("s", 4000)},
		{Role: llm.RoleUser, Content: strings.Repeat("u", 4000)},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "browser_use"}}, Content: strings.Repeat("a", 4000)},
			llm.Message{Role: llm.RoleTool, Name: "browser_use", Content: strings.Repeat("r", 4000)},
		)
	}
	cfg := HistoryCompactConfig{TriggerRatio: 0.75, TrailingBudgetRatio: 0.30, CooldownTokenDelta: 1000}
	st := &compactState{}
	out := compactHistory(context.Background(), msgs, NoopHistoryCompactor{}, 20000, cfg, st)

	if len(out) >= len(msgs) {
		t.Errorf("fallback truncate should shrink msgs, got %d >= %d", len(out), len(msgs))
	}
	if out[0].Role != llm.RoleSystem || out[1].Role != llm.RoleUser {
		t.Errorf("system + first user must be preserved")
	}
}

func TestCompactHistory_CooldownSkipsRepeatedTrigger(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("s", 4000)},
		{Role: llm.RoleUser, Content: strings.Repeat("u", 4000)},
	}
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1", Name: "browser_use"}}, Content: strings.Repeat("a", 4000)},
			llm.Message{Role: llm.RoleTool, Name: "browser_use", Content: strings.Repeat("r", 4000)},
		)
	}
	cfg := HistoryCompactConfig{TriggerRatio: 0.75, TrailingBudgetRatio: 0.30, CooldownTokenDelta: 100000}
	st := &compactState{lastCompactedTokens: 21000}
	gen := &stubGen{content: "should not be called"}
	out := compactHistory(context.Background(), msgs, NewLLMHistoryCompactor(gen), 20000, cfg, st)

	if gen.calls != 0 {
		t.Errorf("cooldown should skip LLM call, got %d calls", gen.calls)
	}
	if len(out) != len(msgs) {
		t.Errorf("cooldown should noop, got %d != %d", len(out), len(msgs))
	}
}

func TestCompactHistory_NilCompactorSkips(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleSystem, Content: strings.Repeat("s", 40000)},
		{Role: llm.RoleUser, Content: strings.Repeat("u", 40000)},
	}
	cfg := HistoryCompactConfig{TriggerRatio: 0.75, TrailingBudgetRatio: 0.50, CooldownTokenDelta: 4000}
	st := &compactState{}
	out := compactHistory(context.Background(), msgs, nil, 20000, cfg, st)
	if len(out) != 2 {
		t.Errorf("nil compactor should noop, got len=%d", len(out))
	}
}

// ---- LLMHistoryCompactor.Compact ------------------------------------------

func TestLLMHistoryCompactor_EmptyInput(t *testing.T) {
	c := NewLLMHistoryCompactor(&stubGen{})
	if _, err := c.Compact(context.Background(), nil); err == nil {
		t.Fatal("empty oldTurns should err")
	}
}

func TestLLMHistoryCompactor_GenErrorPropagates(t *testing.T) {
	c := NewLLMHistoryCompactor(&stubGen{err: errors.New("net down")})
	_, err := c.Compact(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "x"}})
	if err == nil || !strings.Contains(err.Error(), "net down") {
		t.Fatalf("want wrapped err containing 'net down', got %v", err)
	}
}

func TestLLMHistoryCompactor_EmptySummaryErrors(t *testing.T) {
	c := NewLLMHistoryCompactor(&stubGen{content: "   "})
	_, err := c.Compact(context.Background(), []llm.Message{{Role: llm.RoleUser, Content: "x"}})
	if err == nil {
		t.Fatal("empty summary should err")
	}
}

func TestLLMHistoryCompactor_WrapsWithTag(t *testing.T) {
	c := NewLLMHistoryCompactor(&stubGen{content: "summary body"})
	out, err := c.Compact(context.Background(), []llm.Message{
		{Role: llm.RoleUser, Content: "x"},
		{Role: llm.RoleAssistant, Content: "y"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Role != llm.RoleUser {
		t.Errorf("summary msg should be user role, got %s", out.Role)
	}
	if !strings.Contains(out.Content, "<历史片段摘要 turns=\"2\">") {
		t.Errorf("missing wrapper tag with turn count: %s", out.Content)
	}
	if !strings.Contains(out.Content, "summary body") {
		t.Errorf("missing summary body: %s", out.Content)
	}
}
