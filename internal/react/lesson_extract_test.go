package react

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/vulnfinding"
	"github.com/V3teran/liusha/internal/lesson"
)

// lessonAdderSpy 在 lesson_extract 测试里替代真 *lesson.Store。
//
// 捕获每次 Add 调用的入参，并允许注入失败模拟降级路径。
type lessonAdderSpy struct {
	mu    sync.Mutex
	calls int
	last  lesson.Lesson
	err   error
}

func (s *lessonAdderSpy) Add(_ context.Context, l lesson.Lesson) (lesson.Lesson, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.last = l
	if s.err != nil {
		return lesson.Lesson{}, s.err
	}
	return l, nil
}

func (s *lessonAdderSpy) snapshot() (int, lesson.Lesson) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.last
}

func makeFinding() vulnfinding.VulnFinding {
	return vulnfinding.VulnFinding{
		ID:           "fid-abc",
		EngagementID: "eid-123",
		Host:         "vulnapp.test",
		Kind:         "sqli",
		Title:        "用户输入未过滤",
		DedupKey:     "host|GET /a|q",
		Evidence:     json.RawMessage(`{"req":"...","resp":"..."}`),
	}
}

func TestLessonExtractHook_AddsLesson(t *testing.T) {
	gen := &mockGen{out: "对 sqli 类似端点优先尝试时间盲注。"}
	spy := &lessonAdderSpy{}

	hook := NewLessonExtractHook(gen, spy)
	hook(context.Background(), "eid-123", makeFinding())

	calls, last := spy.snapshot()
	if calls != 1 {
		t.Fatalf("期望 lesson.Add 调一次，实际 %d", calls)
	}
	if last.Host != "vulnapp.test" {
		t.Fatalf("Host 透传错，实际 %q", last.Host)
	}
	if last.Content == "" {
		t.Fatal("Content 不能为空")
	}
	if last.Priority != 7 {
		t.Fatalf("Priority 应为 7，实际 %d", last.Priority)
	}
	if last.SourceFindingID == nil || *last.SourceFindingID != "fid-abc" {
		t.Fatalf("SourceFindingID 应为 fid-abc，实际 %v", last.SourceFindingID)
	}
}

func TestLessonExtractHook_LLMFailure(t *testing.T) {
	// LLM 调用失败 → 不 panic，不 Add
	gen := &mockGen{err: errors.New("llm 502")}
	spy := &lessonAdderSpy{}

	hook := NewLessonExtractHook(gen, spy)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("LLM 失败不应 panic，实际 %v", r)
		}
	}()
	hook(context.Background(), "eid-123", makeFinding())

	if calls, _ := spy.snapshot(); calls != 0 {
		t.Fatalf("LLM 失败时不应 Add，实际 %d", calls)
	}
}

func TestLessonExtractHook_AddFailureSwallowed(t *testing.T) {
	// lesson.Add 返回错误 → 仅 log warn，不 panic
	gen := &mockGen{out: "提示内容"}
	spy := &lessonAdderSpy{err: errors.New("db down")}

	hook := NewLessonExtractHook(gen, spy)

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("Add 失败不应 panic，实际 %v", r)
		}
	}()
	hook(context.Background(), "eid-123", makeFinding())

	if calls, _ := spy.snapshot(); calls != 1 {
		t.Fatalf("即使失败也应已尝试调用一次，实际 %d", calls)
	}
}

func TestLessonExtractHook_EmptyContentSkipped(t *testing.T) {
	// LLM 返回空字符串 → 不写空 lesson
	gen := &mockGen{out: "   "}
	spy := &lessonAdderSpy{}

	hook := NewLessonExtractHook(gen, spy)
	hook(context.Background(), "eid-123", makeFinding())

	if calls, _ := spy.snapshot(); calls != 0 {
		t.Fatalf("空 content 不应 Add，实际 %d", calls)
	}
}

func TestLessonExtractHook_NilLessonAdderSkipsLLM(t *testing.T) {
	// nil LessonAdder → 跳过整个 extract，不浪费 LLM 调用
	gen := &mockGen{out: "would-be-content"}

	hook := NewLessonExtractHook(gen, nil)
	hook(context.Background(), "eid-123", makeFinding())

	if gen.lastMsgs != nil {
		t.Fatalf("nil lessonAdder 时不应调 LLM，实际调用 msgs=%v", gen.lastMsgs)
	}
}

func TestLessonExtractHook_EmptyHostSkipsLLM(t *testing.T) {
	// finding.Host 空 → 跳过 extract（host_lesson 必填 host 列）
	gen := &mockGen{out: "ok"}
	spy := &lessonAdderSpy{}

	f := makeFinding()
	f.Host = ""

	hook := NewLessonExtractHook(gen, spy)
	hook(context.Background(), "eid-123", f)

	if gen.lastMsgs != nil {
		t.Fatalf("Host 空不应调 LLM，实际 msgs=%v", gen.lastMsgs)
	}
	if calls, _ := spy.snapshot(); calls != 0 {
		t.Fatalf("Host 空不应 Add，实际 %d", calls)
	}
}

// lessonToucherSpy 捕获 TouchByDedup 调用。
type lessonToucherSpy struct {
	mu       sync.Mutex
	calls    int
	lastHost string
	lastDK   string
	err      error
}

func (s *lessonToucherSpy) TouchByDedup(_ context.Context, host, dedupKey string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	s.lastHost = host
	s.lastDK = dedupKey
	if s.err != nil {
		return 0, s.err
	}
	// 默认 spy 返回 1（模拟成功 update），让 hook 不重试
	return 1, nil
}

func TestLessonTouchHook_TouchesByDedup(t *testing.T) {
	spy := &lessonToucherSpy{}
	hook := NewLessonTouchHook(spy)

	hook(context.Background(), "eid-x", makeFinding())

	if spy.calls != 1 {
		t.Fatalf("期望 TouchByDedup 调一次，实际 %d", spy.calls)
	}
	if spy.lastHost != "vulnapp.test" {
		t.Fatalf("host 透传错，实际 %q", spy.lastHost)
	}
	if spy.lastDK != "host|GET /a|q" {
		t.Fatalf("dedup_key 透传错，实际 %q", spy.lastDK)
	}
}

func TestLessonTouchHook_NilToucherNoOp(t *testing.T) {
	hook := NewLessonTouchHook(nil)
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("nil toucher 不应 panic，实际 %v", r)
		}
	}()
	hook(context.Background(), "eid-x", makeFinding())
}

func TestLessonTouchHook_EmptyHostOrDedupNoOp(t *testing.T) {
	spy := &lessonToucherSpy{}
	hook := NewLessonTouchHook(spy)

	f := makeFinding()
	f.Host = ""
	hook(context.Background(), "eid-x", f)
	if spy.calls != 0 {
		t.Fatalf("空 Host 不应 Touch，实际 %d", spy.calls)
	}

	f = makeFinding()
	f.DedupKey = ""
	hook(context.Background(), "eid-x", f)
	if spy.calls != 0 {
		t.Fatalf("空 DedupKey 不应 Touch，实际 %d", spy.calls)
	}
}

// 防止 extract 同步实现未来改成异步时测试假阳性：用一个超时 sanity check。
func TestLessonExtractHook_FinishesWithinDeadline(t *testing.T) {
	gen := &mockGen{out: "ok"}
	spy := &lessonAdderSpy{}
	hook := NewLessonExtractHook(gen, spy)

	done := make(chan struct{})
	go func() {
		hook(context.Background(), "eid-123", makeFinding())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("extract hook 不应阻塞 > 2s")
	}
}
