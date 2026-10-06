// Package bus 提供统一的事件总线基础设施
//
// 支持 Task 级别和 Action 级别的事件路由，用于整个系统的事件驱动架构。
package bus

import (
	"time"

	"github.com/google/uuid"
)

// EventType 定义事件类型
type EventType string

// EventActionCompleted 等枚举定义。
const (
	// EventActionProposed 是 planner 提议新动作。
	// EventActionCompleted 是 executor 完成一个动作。
	EventActionProposed  EventType = "action.proposed"
	EventActionCompleted EventType = "action.completed" // planner run 自然跑完时也发

	// EventAttemptGenerated 是 executor 产出漏洞候选 Attempt（evaluator 复现门消费）。
	EventAttemptGenerated EventType = "attempt.generated"

	// EventVerificationPassed 是复现坐实；EventVerificationRefuted 是复现证伪。
	// 均为 planner 重规划的触发依据。
	EventVerificationPassed  EventType = "verification.passed"
	EventVerificationRefuted EventType = "verification.refuted"
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

// generateEventID 生成事件 ID（uuid，保证全局唯一）
func generateEventID() string {
	return "evt_" + uuid.NewString()
}
