package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/finding"
)

// stateAppenderSpy 在 distill 测试里替代真 *engagement.Store。
//
// 捕获每次 AppendHint 的 entry，并允许注入失败模拟降级路径。
type stateAppenderSpy struct {
	mu      sync.Mutex
	calls   int
	lastEID string
	last    []byte
	err     error
}

func (s *stateAppenderSpy) AppendHint(_ context.Context, eid string, entry []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastEID = eid
	s.last = append([]byte(nil), entry...)
	return s.err
}

func (s *stateAppenderSpy) snapshot() (int, string, []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.lastEID, append([]byte(nil), s.last...)
}

func makeFinding() finding.Finding {
	return finding.Finding{
		EngagementID: "eid-123",
		Kind:         "sqli",
		Title:        "用户输入未过滤",
		DedupKey:     "host|GET /a|q",
		Tool:         "react.sqli.skill",
		Evidence:     json.RawMessage(`{"req":"...","resp":"..."}`),
	}
}

func TestDistillHook_AppendsHint(t *testing.T) {
	gen := &mockGen{out: "对 sqli 类似端点优先尝试时间盲注。"}
	spy := &stateAppenderSpy{}

	hook := NewDistillHook(gen, spy)
	hook(context.Background(), "eid-123", makeFinding())

	calls, eid, entry := spy.snapshot()
	if calls != 1 {
		t.Fatalf("期望 AppendHint 调一次，实际 %d", calls)
	}
	if eid != "eid-123" {
		t.Fatalf("eid 透传错，实际 %q", eid)
	}

	var parsed map[string]any
	if err := json.Unmarshal(entry, &parsed); err != nil {
		t.Fatalf("hint entry 必须是合法 JSON：%v / %s", err, entry)
	}
	if parsed["from_skill"] != "react.sqli.skill" {
		t.Fatalf("from_skill 应为 finding.Tool，实际 %v", parsed["from_skill"])
	}
	if parsed["content"] == "" || parsed["content"] == nil {
		t.Fatalf("content 不能为空，实际 %v", parsed["content"])
	}
	if pri, ok := parsed["priority"].(float64); !ok || int(pri) != 7 {
		t.Fatalf("priority 应为 7，实际 %v", parsed["priority"])
	}
}

func TestDistillHook_LLMFailure(t *testing.T) {
	// LLM 调用失败 → 不 panic，不 AppendHint
	gen := &mockGen{err: errors.New("llm 502")}
	spy := &stateAppenderSpy{}

	hook := NewDistillHook(gen, spy)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LLM 失败不应 panic，实际 %v", r)
		}
	}()
	hook(context.Background(), "eid-123", makeFinding())

	if calls, _, _ := spy.snapshot(); calls != 0 {
		t.Fatalf("LLM 失败时不应 AppendHint，实际 %d", calls)
	}
}

func TestDistillHook_AppendHintFailure(t *testing.T) {
	// AppendHint 返回错误 → 仅 log warn，不 panic
	gen := &mockGen{out: "提示内容"}
	spy := &stateAppenderSpy{err: errors.New("db down")}

	hook := NewDistillHook(gen, spy)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("AppendHint 失败不应 panic，实际 %v", r)
		}
	}()
	hook(context.Background(), "eid-123", makeFinding())

	if calls, _, _ := spy.snapshot(); calls != 1 {
		t.Fatalf("即使失败也应已尝试调用一次，实际 %d", calls)
	}
}

func TestDistillHook_EmptyContentSkipped(t *testing.T) {
	// LLM 返回空字符串 → 不写空 hint
	gen := &mockGen{out: "   "}
	spy := &stateAppenderSpy{}

	hook := NewDistillHook(gen, spy)
	hook(context.Background(), "eid-123", makeFinding())

	if calls, _, _ := spy.snapshot(); calls != 0 {
		t.Fatalf("空 hint 不应 AppendHint，实际 %d", calls)
	}
}

// 防止 distill 同步实现未来改成异步时测试假阳性：用一个超时 sanity check。
func TestDistillHook_FinishesWithinDeadline(t *testing.T) {
	gen := &mockGen{out: "ok"}
	spy := &stateAppenderSpy{}
	hook := NewDistillHook(gen, spy)

	done := make(chan struct{})
	go func() {
		hook(context.Background(), "eid-123", makeFinding())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("distill hook 不应阻塞 > 2s")
	}
}
