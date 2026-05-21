// Package agentrun 实现 agent_run 持久化层：每次 ReAct 调用对应一行，
// 状态机 pending → running → done | error | aborted。
//
// commander / striker 关系（subtask swarm）：
//   - commander / 独立任务：commander_id = NULL
//   - striker：commander_id 指向 commander agent_task.id；striker **不**入 asynq，
//     由 internal/subtask 包在 commander goroutine 内手动调 Store.Create 写入。
//     list_strikers 工具从 subtask.Registry 内存读，不查 PG（PG commander_id 列只供
//     viewer 树渲染 + ListByOwner / ListByOwnerID 一并取整个任务树）。
//
// 崩溃恢复语义见 internal/subtask 包注释（Registry 内存态丢失后 striker 不可从 PG 恢复）。
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
// CommanderID 空表示独立/根任务；非空时指向commander agent_task.id（subtask swarm）。
type ReactRun struct {
	ID        string
	OwnerType string // 'passive_session' 或 'active_scan'
	OwnerID   string // passive_session.id / active_scan.id
	CommanderID  string
	Role      string
	Input     json.RawMessage
	Result    json.RawMessage
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewParams 是 Store.Create 的入参。
// CommanderID 留空表示独立/根任务；填值时 INSERT 写入 parent_id 列（subtask 用）。
// OwnerType + OwnerID 必填（0041 之后 NOT NULL）。
type NewParams struct {
	OwnerType string // 'passive_session' / 'active_scan'
	OwnerID   string
	Role      string
	Input     json.RawMessage
	CommanderID  string
}
