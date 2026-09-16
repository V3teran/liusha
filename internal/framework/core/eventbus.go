// EventBus 提供轻量级的进程内事件发布订阅机制。
//
// 设计原则：
// 1. 类型安全：每个事件有明确的类型
// 2. 异步通知：发布者不阻塞
// 3. 生命周期管理：支持订阅者取消订阅
// 4. 容量保护：防止内存泄漏
package core

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// EventType 是事件类型标识。
type EventType string

const (
	// ActionKilled 表示 action 被终止。
	EventActionKilled EventType = "action.killed"
	// ActionSteered 表示 action 被纠偏。
	EventActionSteered EventType = "action.steered"
	// ActionCompleted 表示 action 完成。
	EventActionCompleted EventType = "action.completed"
	// EventHumanInputRequired 表示需要人工输入。
	EventHumanInputRequired EventType = "human.input.required"
	// EventHumanInputReceived 表示收到人工输入。
	EventHumanInputReceived EventType = "human.input.received"
)

// Event 是事件载体。
type Event struct {
	ID        string                 // 事件唯一标识
	Type      EventType              // 事件类型
	ActionID  string                 // 关联的 action ID
	Payload   map[string]interface{} // 事件载荷
	Timestamp time.Time              // 事件时间戳
}

// NewEvent 创建事件。
func NewEvent(typ EventType, actionID string, source string, payload map[string]interface{}) Event {
	return Event{
		ID:        generateEventID(),
		Type:      typ,
		ActionID:  actionID,
		Payload:   payload,
		Timestamp: time.Now(),
	}
}

func generateEventID() string {
	// 简单实现：时间戳 + 随机数
	return fmt.Sprintf("evt_%d_%d", time.Now().UnixNano(), time.Now().Nanosecond()%10000)
}

// EventFilter 是事件过滤器函数。
type EventFilter func(Event) bool

// EventPublisher 定义事件发布能力。
type EventPublisher interface {
	Publish(event Event)
}

// EventSubscriber 定义事件订阅能力。
type EventSubscriber interface {
	Subscribe(ctx context.Context, actionID string) *Subscription
}

// EventBus 是事件总线接口，组合发布和订阅能力。
type EventBus interface {
	EventPublisher
	EventSubscriber
}

// EventStore 定义事件持久化接口。
type EventStore interface {
	// Save 保存事件
	Save(ctx context.Context, event Event) error

	// Load 加载事件（按 actionID 查询）
	Load(ctx context.Context, actionID string) ([]Event, error)

	// LoadByType 按类型加载事件
	LoadByType(ctx context.Context, typ EventType) ([]Event, error)
}

// Subscription 是订阅句柄。
type Subscription struct {
	id     string
	events chan Event
	cancel func()
}

// Events 返回事件通道。
func (s *Subscription) Events() <-chan Event {
	return s.events
}

// Unsubscribe 取消订阅。
func (s *Subscription) Unsubscribe() {
	if s.cancel != nil {
		s.cancel()
	}
}

// Bus 是事件总线实现。
// 实现 EventBus 接口。
type Bus struct {
	mu          sync.RWMutex
	subscribers map[string]*subscriber
	nextID      int
}

// 确保 Bus 实现 EventBus 接口
var _ EventBus = (*Bus)(nil)

type subscriber struct {
	id       string
	actionID string
	events   chan Event
	ctx      context.Context
	cancel   context.CancelFunc
}

// New 创建事件总线。
func New() *Bus {
	return &Bus{
		subscribers: make(map[string]*subscriber),
	}
}

// Subscribe 订阅指定 action 的事件。
// 返回 Subscription，调用者必须在不需要时调用 Unsubscribe。
func (b *Bus) Subscribe(ctx context.Context, actionID string) *Subscription {
	b.mu.Lock()
	defer b.mu.Unlock()

	// 生成唯一 ID
	b.nextID++
	id := actionID + ":" + string(rune(b.nextID))

	// 创建订阅者
	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscriber{
		id:       id,
		actionID: actionID,
		events:   make(chan Event, 10), // 缓冲 10 个事件
		ctx:      subCtx,
		cancel:   cancel,
	}

	b.subscribers[id] = sub

	// 启动清理协程
	go b.cleanup(sub)

	return &Subscription{
		id:     id,
		events: sub.events,
		cancel: func() {
			b.unsubscribe(id)
		},
	}
}

// Publish 发布事件（异步）。
func (b *Bus) Publish(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	b.mu.RLock()
	defer b.mu.RUnlock()

	// 找到所有订阅该 action 的订阅者
	for _, sub := range b.subscribers {
		if sub.actionID == event.ActionID {
			// 非阻塞发送
			select {
			case sub.events <- event:
			default:
				// 通道满，丢弃（防止阻塞）
			}
		}
	}
}

// unsubscribe 取消订阅。
func (b *Bus) unsubscribe(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if sub, ok := b.subscribers[id]; ok {
		sub.cancel()
		close(sub.events)
		delete(b.subscribers, id)
	}
}

// cleanup 清理已取消的订阅。
func (b *Bus) cleanup(sub *subscriber) {
	<-sub.ctx.Done()
	b.unsubscribe(sub.id)
}

// SubscriberCount 返回订阅者数量（用于监控）。
func (b *Bus) SubscriberCount() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}
