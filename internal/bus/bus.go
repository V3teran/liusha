// Package bus 提供统一的事件总线实现
package bus

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// Bus 统一事件总线接口
type Bus interface {
	// Task 级别订阅（返回只读通道）
	SubscribeTask(taskID string) <-chan Event

	// Task 级别取消订阅
	UnsubscribeTask(taskID string)

	// Action 级别订阅（返回 Subscription 对象）
	SubscribeAction(ctx context.Context, actionID string) *Subscription

	// 发布事件
	Publish(event Event)

	// 便捷方法
	PublishActionProposed(taskID, actionID string)
	PublishActionCompleted(taskID, actionID string)
	PublishAttemptGenerated(taskID, actionID string, attempt interface{})
	PublishVerificationPassed(taskID, nodeID string)
	PublishVerificationRefuted(taskID, actionID string)
}

// Subscription Action 级别订阅句柄
type Subscription struct {
	id     string
	events chan Event
	cancel func()
}

// Events 返回事件通道
func (s *Subscription) Events() <-chan Event {
	return s.events
}

// Unsubscribe 取消订阅
func (s *Subscription) Unsubscribe() {
	if s.cancel != nil {
		s.cancel()
	}
}

// MemoryBus 内存实现的事件总线
type MemoryBus struct {
	ctx context.Context

	// Task 级别订阅
	taskChannels     map[string]chan Event
	taskRegister     chan string
	taskUnregister   chan string
	taskRegisterDone chan string // 通知通道创建完成

	// Action 级别订阅
	actionMu          sync.RWMutex
	actionSubscribers map[string]*subscriber
	nextSubID         int

	// 发布通道
	publish chan Event
}

type subscriber struct {
	id       string
	actionID string
	events   chan Event
	ctx      context.Context
	cancel   context.CancelFunc
}

// New 创建内存事件总线
func New(ctx context.Context) *MemoryBus {
	bus := &MemoryBus{
		ctx:               ctx,
		taskChannels:      make(map[string]chan Event),
		taskRegister:      make(chan string, 10),
		taskUnregister:    make(chan string, 10),
		taskRegisterDone:  make(chan string, 10),
		actionSubscribers: make(map[string]*subscriber),
		publish:           make(chan Event, 100),
	}
	go bus.run()
	return bus
}

// run 运行事件总线的主循环
func (b *MemoryBus) run() {
	for {
		select {
		case <-b.ctx.Done():
			// 关闭所有 Task 通道
			for _, ch := range b.taskChannels {
				close(ch)
			}
			// 关闭所有 Action 订阅
			b.actionMu.Lock()
			for _, sub := range b.actionSubscribers {
				sub.cancel()
				close(sub.events)
			}
			b.actionMu.Unlock()
			return

		case taskID := <-b.taskRegister:
			if _, exists := b.taskChannels[taskID]; !exists {
				b.taskChannels[taskID] = make(chan Event, 50)
			}
			b.taskRegisterDone <- taskID // 发送完成信号

		case taskID := <-b.taskUnregister:
			if ch, exists := b.taskChannels[taskID]; exists {
				close(ch)
				delete(b.taskChannels, taskID)
			}

		case event := <-b.publish:
			// 设置时间戳
			if event.Timestamp.IsZero() {
				event.Timestamp = time.Now()
			}
			// 生成事件 ID（如果没有）
			if event.ID == "" {
				event.ID = generateEventID()
			}

			// Task 级别分发
			if event.TaskID != "" {
				if ch, exists := b.taskChannels[event.TaskID]; exists {
					select {
					case ch <- event:
					default:
						// 通道满，丢弃（背压处理）
					}
				}
			}

			// Action 级别分发
			if event.ActionID != "" {
				b.actionMu.RLock()
				for _, sub := range b.actionSubscribers {
					if sub.actionID == event.ActionID {
						select {
						case sub.events <- event:
						default:
							// 通道满，丢弃
						}
					}
				}
				b.actionMu.RUnlock()
			}
		}
	}
}

// SubscribeTask 订阅指定 TaskID 的事件
func (b *MemoryBus) SubscribeTask(taskID string) <-chan Event {
	b.taskRegister <- taskID
	<-b.taskRegisterDone // 等待通道创建完成
	return b.taskChannels[taskID]
}

// UnsubscribeTask 取消订阅
func (b *MemoryBus) UnsubscribeTask(taskID string) {
	b.taskUnregister <- taskID
}

// SubscribeAction 订阅指定 ActionID 的事件
func (b *MemoryBus) SubscribeAction(ctx context.Context, actionID string) *Subscription {
	b.actionMu.Lock()
	defer b.actionMu.Unlock()

	// 生成唯一 ID
	b.nextSubID++
	id := fmt.Sprintf("%s:%d", actionID, b.nextSubID)

	// 创建订阅者
	subCtx, cancel := context.WithCancel(ctx)
	sub := &subscriber{
		id:       id,
		actionID: actionID,
		events:   make(chan Event, 10),
		ctx:      subCtx,
		cancel:   cancel,
	}

	b.actionSubscribers[id] = sub

	// 启动清理协程
	go b.cleanupActionSubscriber(sub)

	return &Subscription{
		id:     id,
		events: sub.events,
		cancel: func() {
			b.unsubscribeAction(id)
		},
	}
}

// unsubscribeAction 取消 Action 级别订阅
func (b *MemoryBus) unsubscribeAction(id string) {
	b.actionMu.Lock()
	defer b.actionMu.Unlock()

	if sub, ok := b.actionSubscribers[id]; ok {
		sub.cancel()
		close(sub.events)
		delete(b.actionSubscribers, id)
	}
}

// cleanupActionSubscriber 清理已取消的订阅
func (b *MemoryBus) cleanupActionSubscriber(sub *subscriber) {
	<-sub.ctx.Done()
	b.unsubscribeAction(sub.id)
}

// Publish 发布事件
func (b *MemoryBus) Publish(event Event) {
	select {
	case b.publish <- event:
	case <-b.ctx.Done():
	}
}

// ============================================
// 便捷方法实现
// ============================================

func (b *MemoryBus) PublishActionProposed(taskID, actionID string) {
	b.Publish(Event{
		Type:     EventActionProposed,
		TaskID:   taskID,
		ActionID: actionID,
		Payload: map[string]interface{}{
			"action_id": actionID,
		},
	})
}

func (b *MemoryBus) PublishActionCompleted(taskID, actionID string) {
	b.Publish(Event{
		Type:     EventActionCompleted,
		TaskID:   taskID,
		ActionID: actionID,
		Payload: map[string]interface{}{
			"action_id": actionID,
		},
	})
}

func (b *MemoryBus) PublishAttemptGenerated(taskID, actionID string, attempt interface{}) {
	b.Publish(Event{
		Type:     EventAttemptGenerated,
		TaskID:   taskID,
		ActionID: actionID,
		Payload: map[string]interface{}{
			"action_id": actionID,
			"attempt":   attempt,
		},
	})
}

func (b *MemoryBus) PublishVerificationPassed(taskID, nodeID string) {
	b.Publish(Event{
		Type:   EventVerificationPassed,
		TaskID: taskID,
		Payload: map[string]interface{}{
			"node_id": nodeID,
		},
	})
}

func (b *MemoryBus) PublishVerificationRefuted(taskID, actionID string) {
	b.Publish(Event{
		Type:     EventVerificationRefuted,
		TaskID:   taskID,
		ActionID: actionID,
		Payload: map[string]interface{}{
			"action_id": actionID,
		},
	})
}
