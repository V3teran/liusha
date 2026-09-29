package executor

import (
	"time"
)

// ActionMetadata 是 action 节点的 metadata 结构（与 planner 保持一致）。
type ActionMetadata struct {
	SteeringMessages []SteeringMessage `json:"steering_messages,omitempty"`
	KilledReason     *KilledReason     `json:"killed_reason,omitempty"`
}

// SteeringMessage 是纠偏消息。
type SteeringMessage struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "planner" | "self"
	Guidance  string    `json:"guidance"`
	Applied   bool      `json:"applied"` // 是否已应用
}

// KilledReason 是 Kill 原因。
type KilledReason struct {
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source"` // "planner" | "actor"
	Reason    string    `json:"reason"`
}

