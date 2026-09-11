package middleware

import (
	"context"
	"sync"
	"time"
)

// StreamBuffer 是流式缓冲器。
type StreamBuffer struct {
	bufferSize int
	flushInterval time.Duration
}

// NewStreamBuffer 创建流式缓冲器。
func NewStreamBuffer(bufferSize int, flushInterval time.Duration) *StreamBuffer {
	return &StreamBuffer{
		bufferSize:    bufferSize,
		flushInterval: flushInterval,
	}
}

// Buffer 缓冲流（背压处理）。
func (b *StreamBuffer) Buffer(ctx context.Context, input <-chan StreamEvent) <-chan StreamEvent {
	output := make(chan StreamEvent, b.bufferSize)

	go func() {
		defer close(output)

		buffer := make([]StreamEvent, 0, b.bufferSize)
		ticker := time.NewTicker(b.flushInterval)
		defer ticker.Stop()

		flush := func() {
			for _, event := range buffer {
				select {
				case output <- event:
				case <-ctx.Done():
					return
				}
			}
			buffer = buffer[:0]
		}

		for {
			select {
			case event, ok := <-input:
				if !ok {
					flush()
					return
				}

				buffer = append(buffer, event)

				// 缓冲区满，立即刷新
				if len(buffer) >= b.bufferSize {
					flush()
				}

			case <-ticker.C:
				// 定时刷新
				flush()

			case <-ctx.Done():
				flush()
				return
			}
		}
	}()

	return output
}

// BufferWithBackpressure 带背压的缓冲（慢消费者丢弃旧事件）。
func (b *StreamBuffer) BufferWithBackpressure(ctx context.Context, input <-chan StreamEvent) <-chan StreamEvent {
	output := make(chan StreamEvent, b.bufferSize)

	go func() {
		defer close(output)

		for {
			select {
			case event, ok := <-input:
				if !ok {
					return
				}

				// 非阻塞发送
				select {
				case output <- event:
					// 发送成功
				default:
					// 缓冲区满，丢弃最旧的事件
					select {
					case <-output:
						// 丢弃一个旧事件
					default:
					}

					select {
					case output <- event:
					default:
						// 仍然无法发送，跳过
					}
				}

			case <-ctx.Done():
				return
			}
		}
	}()

	return output
}

// ============================================
// 高级缓冲策略
// ============================================

// RingBuffer 环形缓冲器（固定大小，覆盖旧数据）。
type RingBuffer struct {
	size   int
	buffer []StreamEvent
	head   int
	tail   int
	count  int
	mu     sync.RWMutex
}

// NewRingBuffer 创建环形缓冲器。
func NewRingBuffer(size int) *RingBuffer {
	return &RingBuffer{
		size:   size,
		buffer: make([]StreamEvent, size),
	}
}

// Push 添加事件。
func (r *RingBuffer) Push(event StreamEvent) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.buffer[r.tail] = event
	r.tail = (r.tail + 1) % r.size

	if r.count < r.size {
		r.count++
	} else {
		// 覆盖最旧的数据
		r.head = (r.head + 1) % r.size
	}
}

// Pop 取出最旧的事件。
func (r *RingBuffer) Pop() (StreamEvent, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.count == 0 {
		return StreamEvent{}, false
	}

	event := r.buffer[r.head]
	r.head = (r.head + 1) % r.size
	r.count--

	return event, true
}

// Peek 查看最旧的事件（不移除）。
func (r *RingBuffer) Peek() (StreamEvent, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return StreamEvent{}, false
	}

	return r.buffer[r.head], true
}

// Len 返回当前缓冲区大小。
func (r *RingBuffer) Len() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.count
}

// Clear 清空缓冲区。
func (r *RingBuffer) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.head = 0
	r.tail = 0
	r.count = 0
}

// ToSlice 返回所有事件（从旧到新）。
func (r *RingBuffer) ToSlice() []StreamEvent {
	r.mu.RLock()
	defer r.mu.RUnlock()

	if r.count == 0 {
		return nil
	}

	result := make([]StreamEvent, r.count)
	for i := 0; i < r.count; i++ {
		idx := (r.head + i) % r.size
		result[i] = r.buffer[idx]
	}

	return result
}

// ============================================
// 优先级缓冲器
// ============================================

// PriorityBuffer 优先级缓冲器（高优先级事件先发送）。
type PriorityBuffer struct {
	queues map[int][]StreamEvent // priority -> events
	mu     sync.RWMutex
}

// NewPriorityBuffer 创建优先级缓冲器。
func NewPriorityBuffer() *PriorityBuffer {
	return &PriorityBuffer{
		queues: make(map[int][]StreamEvent),
	}
}

// Push 添加事件。
func (p *PriorityBuffer) Push(event StreamEvent, priority int) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.queues[priority] = append(p.queues[priority], event)
}

// Pop 取出最高优先级的事件。
func (p *PriorityBuffer) Pop() (StreamEvent, int, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	// 找到最高优先级
	maxPriority := -1
	for priority := range p.queues {
		if len(p.queues[priority]) > 0 && priority > maxPriority {
			maxPriority = priority
		}
	}

	if maxPriority == -1 {
		return StreamEvent{}, 0, false
	}

	// 取出事件
	queue := p.queues[maxPriority]
	event := queue[0]
	p.queues[maxPriority] = queue[1:]

	return event, maxPriority, true
}

// Len 返回缓冲区总大小。
func (p *PriorityBuffer) Len() int {
	p.mu.RLock()
	defer p.mu.RUnlock()

	total := 0
	for _, queue := range p.queues {
		total += len(queue)
	}
	return total
}
