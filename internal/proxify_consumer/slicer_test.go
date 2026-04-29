package proxify_consumer

import (
	"context"
	"sync"
	"testing"
	"time"
)

// recorder 记录 appendFlow 的调用次数与传入的 flowID。
type recorder struct {
	mu    sync.Mutex
	calls int
	ids   []int64
}

func (r *recorder) appendFlow(_ context.Context, id int64) (string, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	r.ids = append(r.ids, id)
	return "w", true, nil
}

// TestSlicer_FlushOnAge 验证：低于 batch 阈值时，达到 maxAge 后 ticker 触发 flush。
func TestSlicer_FlushOnAge(t *testing.T) {
	rec := &recorder{}
	s := newSlicer(rec.appendFlow, nil, 100, 50*time.Millisecond)
	defer s.stop()
	s.onFlow(context.Background(), 1)
	time.Sleep(150 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.calls < 1 {
		t.Fatalf("expected at least 1 append, got %d", rec.calls)
	}
}

// TestSlicer_OnClose 验证：appendFlow 返回 closed=true 时，onClose 被调用且收到 windowID。
func TestSlicer_OnClose(t *testing.T) {
	rec := &recorder{}
	var (
		mu     sync.Mutex
		closed []string
	)
	s := newSlicer(rec.appendFlow, func(_ context.Context, id string) {
		mu.Lock()
		defer mu.Unlock()
		closed = append(closed, id)
	}, 100, 50*time.Millisecond)
	defer s.stop()
	s.onFlow(context.Background(), 1)
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(closed) != 1 || closed[0] != "w" {
		t.Fatalf("closed=%v", closed)
	}
}

// TestSlicer_FlushOnBatch 验证：累积到 maxBatch 立刻 flush，不等 ticker。
func TestSlicer_FlushOnBatch(t *testing.T) {
	rec := &recorder{}
	s := newSlicer(rec.appendFlow, nil, 3, time.Hour)
	defer s.stop()
	for i := int64(1); i <= 3; i++ {
		s.onFlow(context.Background(), i)
	}
	// 给 flush 一点时间（synchronous 调用，但保险起见）
	time.Sleep(20 * time.Millisecond)
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if rec.calls != 3 {
		t.Fatalf("expected 3 appends after batch fill, got %d", rec.calls)
	}
}
