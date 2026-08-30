// Package cognition 实现 L4 认知循环（PAE: Plan-Act-Evolve）的事件驱动基础设施。
// Planner Agent 通过事件通道被唤醒重新规划，而非轮询。
package orchestrator

import (
	"context"
	"time"
)

// EventType 定义触发 Planner 重新规划的事件类型
type EventType string

const (
	// EventActionCompleted 当一个 Action 执行完成时触发
	EventActionCompleted EventType = "action_completed"

	// EventFindingDiscovered 当发现新的 Finding 时触发
	EventFindingDiscovered EventType = "finding_discovered"

	// EventVerificationPassed 当 Verifier 验证通过，状态晋升到世界模型时触发
	EventVerificationPassed EventType = "verification_passed"

	// EventManualGuidance 当用户手动介入提供指导时触发
	EventManualGuidance EventType = "manual_guidance"

	// EventTaskStarted 当任务首次启动时触发（初始规划）
	EventTaskStarted EventType = "task_started"

	// EventHeartbeat 定期心跳，用于检查是否需要重新评估
	EventHeartbeat EventType = "heartbeat"
)

// Event 表示一个触发 Planner 重新规划的事件
type Event struct {
	Type      EventType              `json:"type"`
	TaskID    string                 `json:"task_id"`
	Timestamp time.Time              `json:"timestamp"`
	Payload   map[string]interface{} `json:"payload,omitempty"`
}

// EventBus 管理事件的发布和订阅
type EventBus struct {
	// 每个 TaskID 一个事件通道
	channels map[string]chan Event
	// 用于通知新任务注册
	register   chan string
	unregister chan string
	// 发布事件
	publish chan Event
	// 上下文取消
	ctx context.Context
}

// NewEventBus 创建新的事件总线
func NewEventBus(ctx context.Context) *EventBus {
	bus := &EventBus{
		channels:   make(map[string]chan Event),
		register:   make(chan string, 10),
		unregister: make(chan string, 10),
		publish:    make(chan Event, 100),
		ctx:        ctx,
	}
	go bus.run()
	return bus
}

// run 运行事件总线的主循环
func (b *EventBus) run() {
	for {
		select {
		case <-b.ctx.Done():
			// 关闭所有通道
			for _, ch := range b.channels {
				close(ch)
			}
			return

		case taskID := <-b.register:
			if _, exists := b.channels[taskID]; !exists {
				b.channels[taskID] = make(chan Event, 50)
			}

		case taskID := <-b.unregister:
			if ch, exists := b.channels[taskID]; exists {
				close(ch)
				delete(b.channels, taskID)
			}

		case event := <-b.publish:
			if ch, exists := b.channels[event.TaskID]; exists {
				select {
				case ch <- event:
				default:
					// 通道满，丢弃旧事件（背压处理）
				}
			}
		}
	}
}

// Subscribe 订阅指定 TaskID 的事件（Planner Agent 调用）
func (b *EventBus) Subscribe(taskID string) <-chan Event {
	b.register <- taskID
	// 等待通道创建
	time.Sleep(10 * time.Millisecond)
	return b.channels[taskID]
}

// Unsubscribe 取消订阅（Task 结束时调用）
func (b *EventBus) Unsubscribe(taskID string) {
	b.unregister <- taskID
}

// Publish 发布事件
func (b *EventBus) Publish(event Event) {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}
	select {
	case b.publish <- event:
	case <-b.ctx.Done():
	}
}

// PublishActionCompleted 发布 Action 完成事件
func (b *EventBus) PublishActionCompleted(taskID string, actionID string) {
	b.Publish(Event{
		Type:   EventActionCompleted,
		TaskID: taskID,
		Payload: map[string]interface{}{
			"action_id": actionID,
		},
	})
}

// PublishFindingDiscovered 发布 Finding 发现事件
func (b *EventBus) PublishFindingDiscovered(taskID string, findingID string) {
	b.Publish(Event{
		Type:   EventFindingDiscovered,
		TaskID: taskID,
		Payload: map[string]interface{}{
			"finding_id": findingID,
		},
	})
}

// PublishVerificationPassed 发布验证通过事件
func (b *EventBus) PublishVerificationPassed(taskID string, nodeID string) {
	b.Publish(Event{
		Type:   EventVerificationPassed,
		TaskID: taskID,
		Payload: map[string]interface{}{
			"node_id": nodeID,
		},
	})
}

// PublishManualGuidance 发布人工干预事件
func (b *EventBus) PublishManualGuidance(taskID string, guidance string) {
	b.Publish(Event{
		Type:   EventManualGuidance,
		TaskID: taskID,
		Payload: map[string]interface{}{
			"guidance": guidance,
		},
	})
}

// PublishTaskStarted 发布任务启动事件
func (b *EventBus) PublishTaskStarted(taskID string) {
	b.Publish(Event{
		Type:   EventTaskStarted,
		TaskID: taskID,
	})
}

// PublishHeartbeat 发布心跳事件（定期触发）
func (b *EventBus) PublishHeartbeat(taskID string) {
	b.Publish(Event{
		Type:   EventHeartbeat,
		TaskID: taskID,
	})
}
