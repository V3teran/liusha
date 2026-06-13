package main

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/einoagent"
)

// newTestSink 构造一个注入 fake writeFn 的 sink（不碰 DB/redis），并启动 writer goroutine。
// 返回 sink + 一个取已处理事件快照的函数（writeFn 内加锁记录，单 writer 串行调用）。
func newTestSink(buf int) (*einoEventSink, func() []einoagent.ScanEvent) {
	var mu sync.Mutex
	var got []einoagent.ScanEvent
	s := &einoEventSink{
		ch:   make(chan einoagent.ScanEvent, buf),
		done: make(chan struct{}),
	}
	s.writeFn = func(ev einoagent.ScanEvent) {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}
	go s.run()
	snapshot := func() []einoagent.ScanEvent {
		mu.Lock()
		defer mu.Unlock()
		out := make([]einoagent.ScanEvent, len(got))
		copy(out, got)
		return out
	}
	return s, snapshot
}

// TestEventSink_FlushAllInOrder 锁住核心契约：串行入队的事件，Close 后全部处理且保持顺序。
func TestEventSink_FlushAllInOrder(t *testing.T) {
	s, snapshot := newTestSink(8)
	const n = 50
	for i := 0; i < n; i++ {
		s.OnScanEvent(context.Background(), einoagent.ScanEvent{ToolName: "t", Args: string(rune('A' + i%26)), DurationMs: i})
	}
	s.Close() // 关 channel + 等 writer 写完缓冲

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

// TestEventSink_DropOnCtxCancel 验证 run ctx 取消时 OnScanEvent 不阻塞、丢弃在途事件。
// 用容量 0 的 channel 制造"队列满"，再传已取消的 ctx —— 应走 ctx.Done 分支立即返回（不死锁）。
func TestEventSink_DropOnCtxCancel(t *testing.T) {
	s, _ := newTestSink(0) // 无缓冲，writer 在消费前 send 会阻塞
	defer s.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	done := make(chan struct{})
	go func() {
		// 连发多条；ctx 已取消 → 每条都应走 ctx.Done 分支立即返回，不阻塞。
		for i := 0; i < 5; i++ {
			s.OnScanEvent(ctx, einoagent.ScanEvent{ToolName: "x"})
		}
		close(done)
	}()

	select {
	case <-done:
		// ok：取消时未阻塞
	case <-time.After(2 * time.Second):
		t.Fatal("ctx 取消时 OnScanEvent 阻塞了（应丢弃在途事件立即返回）")
	}
}
