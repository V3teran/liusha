// Package task 实现 agent_task 持久化层：每一次主 ReAct 调用对应一行，
// 状态机 pending → running → done|error|aborted。
//
// v1.1：parent_task_id 列已删（子 ReAct 同进程嵌套不入 PG，无父子关系）。
package task

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

// Task 是 agent_task 表行的 Go 表示。Result 在终态前为空 jsonb '{}'。
type Task struct {
	ID           string
	EngagementID string
	Role         string
	Skill        string
	Input        json.RawMessage
	Budget       json.RawMessage
	Result       json.RawMessage
	Status       Status
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// NewParams 是 Store.Create 的入参。Skill / Budget 都可选。
type NewParams struct {
	EngagementID string
	Role         string
	Skill        string
	Input        json.RawMessage
	Budget       json.RawMessage
}
