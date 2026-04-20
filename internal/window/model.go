package window

import (
	"time"

	"github.com/google/uuid"
)

type TriggerKind string

const (
	TriggerTime      TriggerKind = "time"
	TriggerFlowCount TriggerKind = "flow_count"
	TriggerManual    TriggerKind = "manual"
)

type AnalysisWindow struct {
	ID           uuid.UUID
	EngagementID uuid.UUID
	TriggerKind  TriggerKind
	FlowCount    int
	StartedAt    time.Time
	EndedAt      *time.Time
}

type TaskStatus string

const (
	TaskPending   TaskStatus = "pending"
	TaskRunning   TaskStatus = "running"
	TaskSucceeded TaskStatus = "succeeded"
	TaskFailed    TaskStatus = "failed"
	TaskCancelled TaskStatus = "cancelled"
)

type AgentTask struct {
	ID            uuid.UUID
	WindowID      uuid.UUID
	Role          string
	Status        TaskStatus
	InputNodeIDs  []uuid.UUID
	OutputNodeIDs []uuid.UUID
	CostMicroUSD  int64
	CreatedAt     time.Time
	StartedAt     *time.Time
	EndedAt       *time.Time
}
