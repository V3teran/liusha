package proxy

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// mockSink 收集每次 Flush 的 batch；线程安全。
type mockSink struct {
	mu       sync.Mutex
	batches  [][]*TrafficSnapshot
	calls    atomic.Int32
	flushed  atomic.Int32 // 累计 snapshot 条数
	notifyCh chan struct{}
}

func newMockSink() *mockSink {
	return &mockSink{notifyCh: make(chan struct{}, 64)}
}

func (m *mockSink) Flush(_ context.Context, snaps []*TrafficSnapshot) error {
	m.mu.Lock()
	cp := make([]*TrafficSnapshot, len(snaps))
	copy(cp, snaps)
	m.batches = append(m.batches, cp)
	m.mu.Unlock()
	m.calls.Add(1)
	m.flushed.Add(int32(len(snaps)))
	select {
	case m.notifyCh <- struct{}{}:
	default:
	}
	return nil
}

func (m *mockSink) waitFlush(t *testing.T, timeout time.Duration) {
	t.Helper()
	select {
	case <-m.notifyCh:
	case <-time.After(timeout):
		t.Fatalf("等待 flush 超时（%s）", timeout)
	}
}

// snapWithBody 用不同 body 构造唯一 snapshot，避免误中去重。
func snapWithBody(i int) *TrafficSnapshot {
	return &TrafficSnapshot{
		Method:      "POST",
		Host:        "vulnapp",
		URI:         "/api",
		RequestBody: []byte{byte(i & 0xff), byte((i >> 8) & 0xff), byte((i >> 16) & 0xff)},
		Timestamp:   time.Now(),
	}
}

func TestAgg_Add_BatchTrigger(t *testing.T) {
	sink := newMockSink()
	dedup := NewTrafficDeduplicator()
	agg := NewAggregator(1*time.Hour, 60*time.Second, 5, dedup, sink)
	// 不 Start，专测 count 触发分支

	for i := 0; i < 5; i++ {
		agg.Add(snapWithBody(i))
	}
	sink.waitFlush(t, 500*time.Millisecond)

	if got := sink.calls.Load(); got != 1 {
		t.Fatalf("Flush 被调=%d want 1", got)
	}
	if got := sink.flushed.Load(); got != 5 {
		t.Fatalf("flushed=%d want 5", got)
	}
	if pending := agg.Pending(); pending != 0 {
		t.Fatalf("pending=%d want 0", pending)
	}
}

func TestAgg_TimerTrigger(t *testing.T) {
	sink := newMockSink()
	dedup := NewTrafficDeduplicator()
	agg := NewAggregator(80*time.Millisecond, 60*time.Second, 1000, dedup, sink)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agg.Start(ctx)
	defer agg.Stop()

	agg.Add(snapWithBody(1))
	sink.waitFlush(t, 1*time.Second)

	if got := sink.flushed.Load(); got < 1 {
		t.Fatalf("timer 触发 flushed=%d want>=1", got)
	}
}

func TestAgg_DedupSkip(t *testing.T) {
	sink := newMockSink()
	dedup := NewTrafficDeduplicator()
	// 用大 buffer + 长 timer，避免被 batch/timer 触发干扰
	agg := NewAggregator(1*time.Hour, 60*time.Second, 1000, dedup, sink)

	s := snapWithBody(7)
	agg.Add(s)
	agg.Add(s) // 完全相同 → 应被去重
	agg.Add(s)

	if got := agg.Pending(); got != 1 {
		t.Fatalf("buffer=%d want 1（其余两条应被去重）", got)
	}
	if got := agg.TotalDuplicated(); got != 2 {
		t.Fatalf("totalDuplicated=%d want 2", got)
	}
	if got := sink.calls.Load(); got != 0 {
		t.Fatalf("不应触发 flush; calls=%d", got)
	}
}

func TestAgg_StopFlushesPending(t *testing.T) {
	sink := newMockSink()
	dedup := NewTrafficDeduplicator()
	agg := NewAggregator(1*time.Hour, 60*time.Second, 1000, dedup, sink)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agg.Start(ctx)

	for i := 0; i < 3; i++ {
		agg.Add(snapWithBody(i))
	}
	if pending := agg.Pending(); pending != 3 {
		t.Fatalf("pending=%d want 3", pending)
	}

	agg.Stop()

	if got := sink.calls.Load(); got != 1 {
		t.Fatalf("Stop 后 Flush 应被调一次, got %d", got)
	}
	if got := sink.flushed.Load(); got != 3 {
		t.Fatalf("Stop flush 数=%d want 3", got)
	}
}

func TestAgg_NilSnapshotIgnored(t *testing.T) {
	sink := newMockSink()
	agg := NewAggregator(1*time.Hour, 60*time.Second, 100, NewTrafficDeduplicator(), sink)
	agg.Add(nil)
	if pending := agg.Pending(); pending != 0 {
		t.Fatalf("nil 不应入缓冲, pending=%d", pending)
	}
}

func TestAgg_StopIdempotent(t *testing.T) {
	agg := NewAggregator(1*time.Hour, 60*time.Second, 100, NewTrafficDeduplicator(), newMockSink())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	agg.Start(ctx)
	agg.Stop()
	agg.Stop() // 不应 panic / double close
}

func TestAgg_NoDedup_NoPanic(t *testing.T) {
	sink := newMockSink()
	agg := NewAggregator(1*time.Hour, 60*time.Second, 2, nil, sink)
	agg.Add(snapWithBody(1))
	agg.Add(snapWithBody(1)) // 相同 body 但 dedup nil → 不去重
	sink.waitFlush(t, 500*time.Millisecond)
	if got := sink.flushed.Load(); got != 2 {
		t.Fatalf("无 dedup 应全入；flushed=%d want 2", got)
	}
}
