package middleware

import (
	"context"
	"sync"
	"time"
)

// StreamAggregator 是流式聚合器，合并多个流。
type StreamAggregator struct {
	batchSize     int
	batchInterval time.Duration
}

// NewStreamAggregator 创建流式聚合器。
func NewStreamAggregator(batchSize int, batchInterval time.Duration) *StreamAggregator {
	return &StreamAggregator{
		batchSize:     batchSize,
		batchInterval: batchInterval,
	}
}

// Aggregate 聚合多个流。
func (a *StreamAggregator) Aggregate(ctx context.Context, streams []<-chan StreamEvent) <-chan StreamEvent {
	output := make(chan StreamEvent, 100)

	var wg sync.WaitGroup
	wg.Add(len(streams))

	// 为每个输入流启动一个 goroutine
	for _, stream := range streams {
		go func(input <-chan StreamEvent) {
			defer wg.Done()

			for {
				select {
				case event, ok := <-input:
					if !ok {
						return // 输入流关闭
					}

					select {
					case output <- event:
					case <-ctx.Done():
						return
					}

				case <-ctx.Done():
					return
				}
			}
		}(stream)
	}

	// 等待所有输入流关闭后关闭输出流
	go func() {
		wg.Wait()
		close(output)
	}()

	return output
}

// Batch 批量处理流（合并相邻事件）。
func (a *StreamAggregator) Batch(ctx context.Context, input <-chan StreamEvent) <-chan []StreamEvent {
	output := make(chan []StreamEvent, 10)

	go func() {
		defer close(output)

		batch := make([]StreamEvent, 0, a.batchSize)
		timer := time.NewTimer(a.batchInterval)
		defer timer.Stop()

		flush := func() {
			if len(batch) > 0 {
				select {
				case output <- batch:
					batch = make([]StreamEvent, 0, a.batchSize)
				case <-ctx.Done():
					return
				}
			}
		}

		for {
			select {
			case event, ok := <-input:
				if !ok {
					flush()
					return
				}

				batch = append(batch, event)

				// 达到批量大小，立即刷新
				if len(batch) >= a.batchSize {
					flush()
					timer.Reset(a.batchInterval)
				}

			case <-timer.C:
				// 超时，刷新当前批次
				flush()
				timer.Reset(a.batchInterval)

			case <-ctx.Done():
				flush()
				return
			}
		}
	}()

	return output
}

// Merge 合并多个事件为一个。
func (a *StreamAggregator) Merge(events []StreamEvent) StreamEvent {
	if len(events) == 0 {
		return StreamEvent{}
	}

	if len(events) == 1 {
		return events[0]
	}

	// 合并策略：使用第一个事件的元信息，合并所有事件的数据
	merged := events[0]
	merged.Type = "batch"

	// 将所有事件数据放入数组
	dataList := make([]any, len(events))
	for i, event := range events {
		dataList[i] = event.Data
	}
	merged.Data = dataList

	// 标记为最终事件
	merged.Final = events[len(events)-1].Final

	return merged
}

// Debounce 防抖处理（快速连续的事件只保留最后一个）。
func (a *StreamAggregator) Debounce(ctx context.Context, input <-chan StreamEvent, wait time.Duration) <-chan StreamEvent {
	output := make(chan StreamEvent, cap(input))

	go func() {
		defer close(output)

		var pending *StreamEvent
		timer := time.NewTimer(wait)
		timer.Stop()

		for {
			select {
			case event, ok := <-input:
				if !ok {
					// 输入流关闭，发送最后的待处理事件
					if pending != nil {
						select {
						case output <- *pending:
						case <-ctx.Done():
						}
					}
					return
				}

				// 更新待处理事件
				pending = &event
				timer.Reset(wait)

			case <-timer.C:
				// 超时，发送待处理事件
				if pending != nil {
					select {
					case output <- *pending:
						pending = nil
					case <-ctx.Done():
						return
					}
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return output
}

// Throttle 节流处理（限制事件发送频率）。
func (a *StreamAggregator) Throttle(ctx context.Context, input <-chan StreamEvent, rate time.Duration) <-chan StreamEvent {
	output := make(chan StreamEvent, cap(input))

	go func() {
		defer close(output)

		ticker := time.NewTicker(rate)
		defer ticker.Stop()

		for {
			select {
			case event, ok := <-input:
				if !ok {
					return
				}

				// 等待下一个时间槽
				select {
				case <-ticker.C:
					select {
					case output <- event:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return output
}

// Window 窗口聚合（固定时间窗口内的事件）。
func (a *StreamAggregator) Window(ctx context.Context, input <-chan StreamEvent, windowSize time.Duration) <-chan []StreamEvent {
	output := make(chan []StreamEvent, 10)

	go func() {
		defer close(output)

		window := make([]StreamEvent, 0)
		ticker := time.NewTicker(windowSize)
		defer ticker.Stop()

		flush := func() {
			if len(window) > 0 {
				select {
				case output <- window:
					window = make([]StreamEvent, 0)
				case <-ctx.Done():
				}
			}
		}

		for {
			select {
			case event, ok := <-input:
				if !ok {
					flush()
					return
				}

				window = append(window, event)

			case <-ticker.C:
				flush()

			case <-ctx.Done():
				flush()
				return
			}
		}
	}()

	return output
}
