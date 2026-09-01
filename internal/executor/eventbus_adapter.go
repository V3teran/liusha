// Package executor 提供 EventBus 适配器。
package executor

import (
	"context"

	"github.com/V3teran/liusha/internal/eventbus"
)

// eventBusAdapter 将 eventbus.Bus 适配为 actor.EventBus 接口。
type eventBusAdapter struct {
	bus *eventbus.Bus
}

// NewEventBusAdapter 创建适配器。
func NewEventBusAdapter(bus *eventbus.Bus) EventBus {
	if bus == nil {
		return nil
	}
	return &eventBusAdapter{bus: bus}
}

// Subscribe 订阅事件。
func (a *eventBusAdapter) Subscribe(ctx context.Context, actionID string) EventSubscription {
	sub := a.bus.Subscribe(ctx, actionID)
	return &eventSubscriptionAdapter{sub: sub}
}

// Publish 发布事件。
func (a *eventBusAdapter) Publish(event ControlEvent) {
	a.bus.Publish(eventbus.Event{
		Type:      eventbus.EventType(event.Type),
		ActionID:  event.ActionID,
		Payload:   event.Payload,
		Timestamp: event.Timestamp,
	})
}

// eventSubscriptionAdapter 适配订阅句柄。
type eventSubscriptionAdapter struct {
	sub *eventbus.Subscription
}

// Events 返回事件通道。
func (a *eventSubscriptionAdapter) Events() <-chan ControlEvent {
	// 创建转换通道
	out := make(chan ControlEvent, 10)
	go func() {
		defer close(out)
		for evt := range a.sub.Events() {
			out <- ControlEvent{
				Type:      string(evt.Type),
				ActionID:  evt.ActionID,
				Payload:   evt.Payload,
				Timestamp: evt.Timestamp,
			}
		}
	}()
	return out
}

// Unsubscribe 取消订阅。
func (a *eventSubscriptionAdapter) Unsubscribe() {
	a.sub.Unsubscribe()
}
