package proxify_consumer

import (
	"context"
	"sync"
	"time"
)

// appendFn 把单个 flowID 推入 window store；返回 (windowID, justClosed, err)。
// 当 OpenOrAppend 因 batch 满而把窗口置为 closed 时，justClosed=true。
type appendFn func(ctx context.Context, flowID int64) (string, bool, error)

// onCloseFn 在窗口刚关闭时回调（参数为关闭的 windowID）；用于 enqueue sniffer。
type onCloseFn func(ctx context.Context, windowID string)

// slicer 把 onFlow 进来的 flowID 缓冲到 maxBatch 或 maxAge 任一触发条件，
// 然后逐条调用 appendFn；每条返回 closed=true 时调一次 onClose。
//
// 注意：本地 maxBatch 用于控制 flush 节奏，window 真正的 close 阈值由 store 决定。
type slicer struct {
	appendFlow appendFn
	onClose    onCloseFn
	maxBatch   int
	maxAge     time.Duration
	stopCh     chan struct{}
	stopOnce   sync.Once
	mu         sync.Mutex
	pending    []int64
}

// newSlicer 构造一个 slicer 并启动后台 ticker。调用方负责在不再使用时调用 stop()。
func newSlicer(a appendFn, onClose onCloseFn, batch int, age time.Duration) *slicer {
	s := &slicer{
		appendFlow: a,
		onClose:    onClose,
		maxBatch:   batch,
		maxAge:     age,
		stopCh:     make(chan struct{}),
	}
	go s.loop()
	return s
}

// onFlow 把 flowID 加入待 flush 队列；达到 maxBatch 立刻同步 flush。
func (s *slicer) onFlow(ctx context.Context, id int64) {
	s.mu.Lock()
	s.pending = append(s.pending, id)
	if len(s.pending) >= s.maxBatch {
		ids := s.pending
		s.pending = nil
		s.mu.Unlock()
		s.flush(ctx, ids)
		return
	}
	s.mu.Unlock()
}

// loop 周期性 flush 未满 batch 的 pending；ticker 间隔为 maxAge。
func (s *slicer) loop() {
	t := time.NewTicker(s.maxAge)
	defer t.Stop()
	for {
		select {
		case <-s.stopCh:
			return
		case <-t.C:
			s.mu.Lock()
			ids := s.pending
			s.pending = nil
			s.mu.Unlock()
			if len(ids) > 0 {
				s.flush(context.Background(), ids)
			}
		}
	}
}

// flush 逐条调用 appendFn；每条返回 closed=true 且配置了 onClose 时触发回调。
func (s *slicer) flush(ctx context.Context, ids []int64) {
	for _, id := range ids {
		wid, closed, err := s.appendFlow(ctx, id)
		if err == nil && closed && s.onClose != nil {
			s.onClose(ctx, wid)
		}
	}
}

// stop 终止后台 ticker。可重复调用。
func (s *slicer) stop() {
	s.stopOnce.Do(func() { close(s.stopCh) })
}
