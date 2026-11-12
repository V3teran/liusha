package llminvocation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/V3teran/liusha/internal/config"
)

// newTestStore 构造一个不连真实 DB 的 Store：flush() 遇到空 batch 直接 return，
// 不会 touch pool，故这里传 nil 也能跑——专测 Flush() 的等待行为，不测落库。
func newTestStore(t *testing.T) *Store {
	t.Helper()
	s := NewStoreWithConfig(nil, config.InvocationConfig{
		FlushIntervalMs: 1000, // 与生产默认一致：若 Flush 退化回「盲等」，这里就会跑够 1s 而超时失败
	})
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// 回归：Flush() 曾经是「channel 空了就 sleep(flushInterval+100ms)」，导致每次查询前
// 白等 1.1s（GET /llm/invocations 等只读接口场景 channel 几乎总是空的）。
// 现在改成请求-确认（flushReq channel），没有待落库数据时应几乎零延迟返回。
func TestFlush_NoDataReturnsImmediately(t *testing.T) {
	s := newTestStore(t)

	start := time.Now()
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("Flush 返回错误: %v", err)
	}
	elapsed := time.Since(start)

	// 留够裕度（worker 调度 + channel round-trip），但必须远小于 flushInterval(1s)，
	// 否则说明退化回了盲等实现。
	const maxElapsed = 200 * time.Millisecond
	if elapsed > maxElapsed {
		t.Errorf("Flush 耗时 %v，超过 %v——是否退化回了盲等 flushInterval 的旧实现？", elapsed, maxElapsed)
	}
}

// 连续多次 Flush 都应各自快速返回（模拟 list/stat/facets 三个 handler 各自调用一次）。
func TestFlush_MultipleCallsAllFast(t *testing.T) {
	s := newTestStore(t)

	const rounds = 3
	start := time.Now()
	for i := 0; i < rounds; i++ {
		if err := s.Flush(context.Background()); err != nil {
			t.Fatalf("第 %d 次 Flush 返回错误: %v", i+1, err)
		}
	}
	elapsed := time.Since(start)

	const maxElapsed = 200 * time.Millisecond
	if elapsed > maxElapsed {
		t.Errorf("%d 次连续 Flush 共耗时 %v，超过 %v", rounds, elapsed, maxElapsed)
	}
}

// ctx 取消时 Flush 应立即以 ctx.Err() 返回，不阻塞到超时。
//
// 先做一次正常 Flush 确保 worker 已在等待接收：此时「发出 flush 请求」与「ctx 已取消」
// 两个分支同时就绪，select 随机选择——曾在 CI 上偶发返回 nil。循环多次让竞态必现。
func TestFlush_ContextCanceled(t *testing.T) {
	s := newTestStore(t)
	if err := s.Flush(context.Background()); err != nil {
		t.Fatalf("预热 Flush 失败: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	for i := 0; i < 200; i++ {
		if err := s.Flush(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("第 %d 次：ctx 已取消，Flush 应返回 context.Canceled，得 %v", i+1, err)
		}
	}
}

// Close 之后再调 Flush 不应挂死（worker 已退出，flushReq 发不出去）。
func TestFlush_AfterClose(t *testing.T) {
	s := NewStoreWithConfig(nil, config.InvocationConfig{FlushIntervalMs: 1000})
	if err := s.Close(); err != nil {
		t.Fatalf("Close 失败: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- s.Flush(context.Background()) }()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Close 后 Flush 返回错误: %v", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Close 后 Flush 挂死，未在 500ms 内返回")
	}
}
