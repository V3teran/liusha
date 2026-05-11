// Package reactrun 实现 agent_task 持久化层：每次主 ReAct 调用对应一行，
// 状态机 pending → running → done | error | aborted。
//
// 注意：子 ReAct 同进程嵌套，不入 PG，没有父子关系（无 parent_task_id 列）。
package agentrun

import (
	"encoding/json"
	"time"
)

// Status 是 agent_task.status 的取值。
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusAborted Status = "aborted"
	StatusError   Status = "error"
)

// ReactRun 是 agent_task 表行的 Go 表示。Result 在终态前为空 jsonb '{}'。
type ReactRun struct {
	ID           string
	EngagementID string
	Role         string
	Input        json.RawMessage
	Result       json.RawMessage
	Status       Status
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NewParams 是 Store.Create 的入参。
type NewParams struct {
	EngagementID string
	Role         string
	Input        json.RawMessage
}
