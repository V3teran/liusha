package middleware

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
)

// StreamManager 是流式管理器，管理所有活跃的流。
type StreamManager struct {
	mu      sync.RWMutex
	streams map[string]*streamHandle // taskID -> handle
	logger  zerolog.Logger

	// 全局配置
	defaultBufferSize int
	maxSubscribers    int
}

// streamHandle 是单个流的句柄。
type streamHandle struct {
	taskID      string
	subscribers []chan StreamEvent
	closed      atomic.Bool
	sequence    atomic.Int64
	mu          sync.RWMutex
}

// NewStreamManager 创建流式管理器。
func NewStreamManager(logger zerolog.Logger) *StreamManager {
	return &StreamManager{
		streams:           make(map[string]*streamHandle),
		logger:            logger.With().Str("component", "stream_manager").Logger(),
		defaultBufferSize: 100,
		maxSubscribers:    100,
	}
}

// Stream 开始流式输出（订阅模式）。
func (m *StreamManager) Stream(ctx context.Context, taskID string) (<-chan StreamEvent, error) {
	m.mu.Lock()
	handle, exists := m.streams[taskID]
	if !exists {
		handle = &streamHandle{
			taskID:      taskID,
			subscribers: make([]chan StreamEvent, 0),
		}
		m.streams[taskID] = handle
	}
	m.mu.Unlock()

	// 检查订阅数限制
	handle.mu.Lock()
	if len(handle.subscribers) >= m.maxSubscribers {
		handle.mu.Unlock()
		return nil, ErrTooManySubscribers{TaskID: taskID, Max: m.maxSubscribers}
	}

	// 创建订阅通道
	ch := make(chan StreamEvent, m.defaultBufferSize)
	handle.subscribers = append(handle.subscribers, ch)
	handle.mu.Unlock()

	m.logger.Debug().
		Str("task_id", taskID).
		Int("subscribers", len(handle.subscribers)).
		Msg("new stream subscriber")

	// 自动清理
	go func() {
		<-ctx.Done()
		m.unsubscribe(taskID, ch)
	}()

	return ch, nil
}

// Send 发送流式事件。
func (m *StreamManager) Send(ctx context.Context, event StreamEvent) error {
	m.mu.RLock()
	handle, exists := m.streams[event.TaskID]
	m.mu.RUnlock()

	if !exists {
		return ErrStreamNotFound{TaskID: event.TaskID}
	}

	if handle.closed.Load() {
		return ErrStreamClosed{TaskID: event.TaskID}
	}

	// 分配序列号
	event.Sequence = handle.sequence.Add(1)

	// 广播给所有订阅者
	handle.mu.RLock()
	subscribers := handle.subscribers
	handle.mu.RUnlock()

	for _, ch := range subscribers {
		select {
		case ch <- event:
			// 发送成功
		default:
			// 订阅者缓冲区满，跳过（背压处理）
			m.logger.Warn().
				Str("task_id", event.TaskID).
				Str("event_type", event.Type).
				Msg("subscriber buffer full, dropping event")
		}
	}

	return nil
}

// Close 关闭流。
func (m *StreamManager) Close(taskID string) error {
	m.mu.Lock()
	handle, exists := m.streams[taskID]
	if !exists {
		m.mu.Unlock()
		return ErrStreamNotFound{TaskID: taskID}
	}
	delete(m.streams, taskID)
	m.mu.Unlock()

	// 标记关闭
	handle.closed.Store(true)

	// 关闭所有订阅通道
	handle.mu.Lock()
	for _, ch := range handle.subscribers {
		close(ch)
	}
	handle.subscribers = nil
	handle.mu.Unlock()

	m.logger.Debug().Str("task_id", taskID).Msg("stream closed")
	return nil
}

// unsubscribe 取消订阅。
func (m *StreamManager) unsubscribe(taskID string, ch chan StreamEvent) {
	m.mu.RLock()
	handle, exists := m.streams[taskID]
	m.mu.RUnlock()

	if !exists {
		return
	}

	handle.mu.Lock()
	defer handle.mu.Unlock()

	for i, sub := range handle.subscribers {
		if sub == ch {
			handle.subscribers = append(handle.subscribers[:i], handle.subscribers[i+1:]...)
			close(ch)
			break
		}
	}

	m.logger.Debug().
		Str("task_id", taskID).
		Int("remaining_subscribers", len(handle.subscribers)).
		Msg("stream unsubscribed")
}

// ListActive 列出所有活跃的流。
func (m *StreamManager) ListActive() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	taskIDs := make([]string, 0, len(m.streams))
	for taskID := range m.streams {
		taskIDs = append(taskIDs, taskID)
	}
	return taskIDs
}

// GetStats 获取流的统计信息。
func (m *StreamManager) GetStats(taskID string) (StreamStats, error) {
	m.mu.RLock()
	handle, exists := m.streams[taskID]
	m.mu.RUnlock()

	if !exists {
		return StreamStats{}, ErrStreamNotFound{TaskID: taskID}
	}

	handle.mu.RLock()
	defer handle.mu.RUnlock()

	return StreamStats{
		TaskID:            taskID,
		Subscribers:       len(handle.subscribers),
		EventsPublished:   handle.sequence.Load(),
		Closed:            handle.closed.Load(),
		BufferSize:        m.defaultBufferSize,
	}, nil
}

// SetDefaultBufferSize 设置默认缓冲区大小。
func (m *StreamManager) SetDefaultBufferSize(size int) {
	m.defaultBufferSize = size
}

// SetMaxSubscribers 设置最大订阅数。
func (m *StreamManager) SetMaxSubscribers(max int) {
	m.maxSubscribers = max
}

// StreamStats 是流的统计信息。
type StreamStats struct {
	TaskID          string `json:"task_id"`
	Subscribers     int    `json:"subscribers"`
	EventsPublished int64  `json:"events_published"`
	Closed          bool   `json:"closed"`
	BufferSize      int    `json:"buffer_size"`
}

// ============================================
// 错误类型
// ============================================

// ErrStreamNotFound 流不存在。
type ErrStreamNotFound struct {
	TaskID string
}

func (e ErrStreamNotFound) Error() string {
	return "stream not found: " + e.TaskID
}

// ErrStreamClosed 流已关闭。
type ErrStreamClosed struct {
	TaskID string
}

func (e ErrStreamClosed) Error() string {
	return "stream closed: " + e.TaskID
}

// ErrTooManySubscribers 订阅者过多。
type ErrTooManySubscribers struct {
	TaskID string
	Max    int
}

func (e ErrTooManySubscribers) Error() string {
	return "too many subscribers for stream " + e.TaskID
}
