// Package bus 提供统一的事件总线基础设施
//
// 支持 Task 级别的广播事件路由（同 task 的每个订阅者独立 channel、收全量拷贝），
// 用于认知循环四 agent 的事件驱动协作。
package bus

import (
	"time"

	"github.com/google/uuid"
)

// EventType 定义事件类型
type EventType string

// 事件类型：唤醒信号与遥测，可丢（订阅缓冲满即丢弃）。决定任务走向的控制信号
// （收敛、重规划）不走总线，见 cognition.CompletionDetector.Converge 与
// planner.Agent.RequestReplan。
const (
	// EventActionProposed 是 planner 提议新动作（唤醒执行者）。
	EventActionProposed EventType = "action.proposed"

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
