// Package bus 提供统一的事件总线基础设施
//
// 支持 Task 级别和 Action 级别的事件路由，用于整个系统的事件驱动架构。
package bus

import (
	"context"
	"fmt"
	"time"
)

// EventType 定义事件类型
type EventType string

const (
	// Action 级别事件
	EventActionProposed  EventType = "action.proposed"
	EventActionCompleted EventType = "action.completed"

	// Attempt 级别事件
	EventAttemptGenerated EventType = "attempt.generated"

	// Verification 级别事件
	EventVerificationPassed  EventType = "verification.passed"
	EventVerificationRefuted EventType = "verification.refuted"

	// 人工干预事件（human-in-the-loop middleware 消费）
	EventHumanInputRequired EventType = "human.input.required"
	EventHumanInputReceived EventType = "human.input.received"
)

// Event 统一事件结构
type Event struct {
	ID        string                 `json:"id"`        // 事件唯一标识
	Type      EventType              `json:"type"`      // 事件类型
	TaskID    string                 `json:"task_id"`   // Task 级别路由键
	ActionID  string                 `json:"action_id"` // Action 级别路由键
	Payload   map[string]interface{} `json:"payload"`   // 事件载荷
	Timestamp time.Time              `json:"timestamp"` // 事件时间戳
}

// EventPublisher 定义事件发布能力
type EventPublisher interface {
	Publish(event Event)
}

// EventSubscriber 定义 Action 级别事件订阅能力
type EventSubscriber interface {
	SubscribeAction(ctx context.Context, actionID string) *Subscription
}

// generateEventID 生成事件 ID
func generateEventID() string {
	return fmt.Sprintf("evt_%d_%d", time.Now().UnixNano(), time.Now().Nanosecond()%10000)
}
