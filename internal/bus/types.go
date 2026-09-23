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
	// Task 级别事件
	EventTaskStarted    EventType = "task.started"
	EventTaskCompleted  EventType = "task.completed"
	EventHeartbeat      EventType = "task.heartbeat"

	// Action 级别事件
	EventActionProposed   EventType = "action.proposed"
	EventActionCompleted  EventType = "action.completed"
	EventActionKilled     EventType = "action.killed"
	EventActionSteered    EventType = "action.steered"

	// Attempt 级别事件
	EventAttemptGenerated EventType = "attempt.generated"

	// Observation 级别事件
	EventObservationCreated EventType = "observation.created"

	// Result 级别事件
	EventResultCreated EventType = "result.created"

	// Finding 级别事件
	EventFindingDiscovered EventType = "finding.discovered"

	// Verification 级别事件
	EventVerificationPassed  EventType = "verification.passed"
	EventVerificationFailed  EventType = "verification.failed"
	EventVerificationRefuted EventType = "verification.refuted"

	// 人工干预事件
	EventManualGuidance         EventType = "manual.guidance"
	EventHumanInputRequired     EventType = "human.input.required"
	EventHumanInputReceived     EventType = "human.input.received"

	// Monitor 级别事件
	EventMonitorRequestReplan EventType = "monitor.request_replan"
)

// Event 统一事件结构
type Event struct {
	ID        string                 `json:"id"`         // 事件唯一标识
	Type      EventType              `json:"type"`       // 事件类型
	TaskID    string                 `json:"task_id"`    // Task 级别路由键
	ActionID  string                 `json:"action_id"`  // Action 级别路由键
	Payload   map[string]interface{} `json:"payload"`    // 事件载荷
	Timestamp time.Time              `json:"timestamp"`  // 事件时间戳
}

// NewEvent 创建事件
func NewEvent(typ EventType, actionID string, source string, payload map[string]interface{}) Event {
	return Event{
		ID:        generateEventID(),
		Type:      typ,
		ActionID:  actionID,
		Payload:   payload,
		Timestamp: time.Now(),
	}
}

// EventPublisher 定义事件发布能力
type EventPublisher interface {
	Publish(event Event)
}

// EventSubscriber 定义事件订阅能力
type EventSubscriber interface {
	SubscribeAction(ctx context.Context, actionID string) *Subscription
}

// generateEventID 生成事件 ID
func generateEventID() string {
	return fmt.Sprintf("evt_%d_%d", time.Now().UnixNano(), time.Now().Nanosecond()%10000)
}
