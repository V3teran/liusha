// Package hunter 实现 hunter 表持久化层：每次 hunter ReAct 运行对应一行。
// 状态机 pending → running → done | error | aborted。
//
// 命名层级（v1.1 定型）：
//   - active_scan / passive_session = 业务"扫描任务"（用户视角的 task）
//   - hunter 表 = 每次 hunter ReAct 运行的记录（orchestrator 或 exploitation）
//   - role 列 = trafficAnalysis / orchestrator / exploitation
//
// orchestrator / exploitation 关系（subtask swarm）：
//   - orchestrator / 独立任务：commander_id = NULL
//   - exploitation：commander_id 指向 orchestrator hunter.id；exploitation **不**入 asynq，
//     由 internal/subtask 包在 orchestrator goroutine 内手动调 Store.Create 写入。
//     list_exploitations 工具从 subtask.Registry 内存读，不查 PG（PG commander_id 列只供
//     viewer 树渲染 + ListByOwner 一并取整个任务树）。
//
// 崩溃恢复语义见 internal/subtask 包注释（Registry 内存态丢失后 exploitation 不可从 PG 恢复）。
package hunter

import (
	"encoding/json"
	"time"
)

// Status 是 hunter.status 的取值。
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusAborted Status = "aborted"
	StatusError   Status = "error"
)

// Run 是 hunter 表行的 Go 表示——每次 hunter ReAct 运行的状态快照。
// Result 在终态前为空 jsonb '{}'。
// CommanderID 空表示独立/根任务；非空时指向 orchestrator hunter.id（subtask swarm）。
type Run struct {
	ID          string
	OwnerType   string // 'passive_session' 或 'active_scan'
	OwnerID     string // passive_session.id / active_scan.id
	CommanderID string
	Role        string
	Input       json.RawMessage
	Result      json.RawMessage
	Status      Status
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// NewParams 是 Store.Create 的入参。
// CommanderID 留空表示独立/根任务；填值时 INSERT 写入 commander_id 列（subtask 用）。
// OwnerType + OwnerID 必填（0041 之后 NOT NULL）。
type NewParams struct {
	OwnerType   string
	OwnerID     string
	Role        string
	Input       json.RawMessage
	CommanderID string
}
