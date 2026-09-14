package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// EventSource 事件源实现
// 用于从 Agent 执行过程中产生事件流
type EventSource struct {
	mu          sync.RWMutex
	subscribers map[string]*subscriber
	config      *StreamConfig
	eventQueue  chan *StreamEvent
	closed      bool
}

// subscriber 订阅者
type subscriber struct {
	id      string
	channel chan *StreamEvent
	filter  StreamEventFilter
}

// StreamEventFilter 流式事件过滤器（函数类型）
type StreamEventFilter func(*StreamEvent) bool

// NewEventSource 创建事件源
func NewEventSource(config *StreamConfig) *EventSource {
	if config == nil {
		config = DefaultStreamConfig()
	}

	source := &EventSource{
		subscribers: make(map[string]*subscriber),
		config:      config,
		eventQueue:  make(chan *StreamEvent, config.BufferSize),
		closed:      false,
	}

	// 启动事件分发器
	go source.dispatchEvents()

	return source
}

// Subscribe 订阅事件流
func (s *EventSource) Subscribe(ctx context.Context) (<-chan *StreamEvent, error) {
	return s.SubscribeWithFilter(ctx, nil)
}

// SubscribeWithFilter 订阅事件流（带过滤器）
func (s *EventSource) SubscribeWithFilter(ctx context.Context, filter StreamEventFilter) (<-chan *StreamEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil, fmt.Errorf("事件源已关闭")
	}

	// 生成订阅者 ID
	subscriberID := fmt.Sprintf("sub-%d", time.Now().UnixNano())

	// 创建订阅者通道
	channel := make(chan *StreamEvent, s.config.BufferSize)

	// 注册订阅者
	s.subscribers[subscriberID] = &subscriber{
		id:      subscriberID,
		channel: channel,
		filter:  filter,
	}

	return channel, nil
}

// Unsubscribe 取消订阅
func (s *EventSource) Unsubscribe(subscriberID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	sub, exists := s.subscribers[subscriberID]
	if !exists {
		return fmt.Errorf("订阅者不存在: %s", subscriberID)
	}

	// 关闭订阅者通道
	close(sub.channel)

	// 删除订阅者
	delete(s.subscribers, subscriberID)

	return nil
}

// Emit 发送事件
func (s *EventSource) Emit(event *StreamEvent) error {
	if s.closed {
		return fmt.Errorf("事件源已关闭")
	}

	// 设置时间戳
	if event.Timestamp == 0 {
		event.Timestamp = time.Now().UnixMilli()
	}

	// 将事件放入队列
	select {
	case s.eventQueue <- event:
		return nil
	default:
		// 队列满，根据背压策略处理
		return fmt.Errorf("事件队列已满，事件被丢弃")
	}
}

// EmitMany 批量发送事件
func (s *EventSource) EmitMany(events []*StreamEvent) error {
	for _, event := range events {
		if err := s.Emit(event); err != nil {
			return err
		}
	}
	return nil
}

// Close 关闭事件源
func (s *EventSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.closed {
		return nil
	}

	s.closed = true

	// 关闭事件队列
	close(s.eventQueue)

	// 关闭所有订阅者通道
	for _, sub := range s.subscribers {
		close(sub.channel)
	}

	// 清空订阅者
	s.subscribers = make(map[string]*subscriber)

	return nil
}

// SubscriberCount 返回订阅者数量
func (s *EventSource) SubscriberCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.subscribers)
}

// dispatchEvents 分发事件到所有订阅者
func (s *EventSource) dispatchEvents() {
	for event := range s.eventQueue {
		s.mu.RLock()
		subscribers := make([]*subscriber, 0, len(s.subscribers))
		for _, sub := range s.subscribers {
			subscribers = append(subscribers, sub)
		}
		s.mu.RUnlock()

		// 分发给每个订阅者
		for _, sub := range subscribers {
			// 应用过滤器
			if sub.filter != nil && !sub.filter(event) {
				continue
			}

			// 非阻塞发送
			select {
			case sub.channel <- event:
				// 发送成功
			default:
				// 订阅者通道满，跳过（背压处理）
			}
		}
	}
}

// FilterByType 创建按事件类型过滤的过滤器
func FilterByType(eventTypes ...StreamEventType) StreamEventFilter {
	typeSet := make(map[StreamEventType]bool)
	for _, t := range eventTypes {
		typeSet[t] = true
	}

	return func(event *StreamEvent) bool {
		return typeSet[event.Type]
	}
}

// FilterByID 创建按事件 ID 过滤的过滤器
func FilterByID(ids ...string) StreamEventFilter {
	idSet := make(map[string]bool)
	for _, id := range ids {
		idSet[id] = true
	}

	return func(event *StreamEvent) bool {
		return idSet[event.ID]
	}
}

// CombineFilters 组合多个过滤器（AND 逻辑）
func CombineFilters(filters ...StreamEventFilter) StreamEventFilter {
	return func(event *StreamEvent) bool {
		for _, filter := range filters {
			if filter != nil && !filter(event) {
				return false
			}
		}
		return true
	}
}

// AnyFilter 组合多个过滤器（OR 逻辑）
func AnyFilter(filters ...StreamEventFilter) StreamEventFilter {
	return func(event *StreamEvent) bool {
		for _, filter := range filters {
			if filter != nil && filter(event) {
				return true
			}
		}
		return false
	}
}
