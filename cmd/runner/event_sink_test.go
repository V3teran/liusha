package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/scanagent"
)

func newTestSink(buf int) (*eventSink, func() []scanagent.ScanEvent) {
	var mu sync.Mutex
	var got []scanagent.ScanEvent
	s := &eventSink{
		ch:   make(chan scanagent.ScanEvent, buf),
		done: make(chan struct{}),
	}
	s.writeFn = func(ev scanagent.ScanEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}
	go s.run()
	snapshot := func() []scanagent.ScanEvent {
		mu.Lock()
		defer mu.Unlock()
		out := make([]scanagent.ScanEvent, len(got))
		copy(out, got)
		return out
	}
	return s, snapshot
}

func TestEventSink_FlushAllInOrder(t *testing.T) {
	s, snapshot := newTestSink(8)
	const n = 50
	for i := 0; i < n; i++ {
		s.OnScanEvent(context.Background(), scanagent.ScanEvent{ToolName: "t", Args: string(rune('A' + i%26)), DurationMs: i})
	}
	s.Close()

	got := snapshot()
	if len(got) != n {
		t.Fatalf("Close 后应处理全部 %d 条事件，实得 %d", n, len(got))
	}
	for i := 0; i < n; i++ {
		if got[i].DurationMs != i {
			t.Fatalf("事件 [%d] 顺序错乱：DurationMs=%d，期望 %d", i, got[i].DurationMs, i)
		}
	}
}

func TestEventSink_DropOnCtxCancel(t *testing.T) {
	s, _ := newTestSink(0)
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan struct{})
	go func() {
		for i := 0; i < 5; i++ {
			s.OnScanEvent(ctx, scanagent.ScanEvent{ToolName: "x"})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消时 OnScanEvent 阻塞了（应丢弃在途事件立即返回）")
	}
}
