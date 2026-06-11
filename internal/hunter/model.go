// Package hunter 实现 hunter 表持久化层：每次 hunter ReAct 运行对应一行。
// 状态机 pending → running → done | error | aborted。
//
// 命名层级（v1.1 定型）：
//   - active_scan / passive_session = 业务"扫描任务"（用户视角的 task）
//   - hunter 表 = 每次 hunter ReAct 运行的记录（orchestrator 或 exploitation）
//   - role 列 = trafficAnalysis / orchestrator / exploitation
//
// orchestrator_id 列（任务树关系）：
//   - orchestrator / 独立任务：orchestrator_id = NULL
//   - 现行 active 路径用 eino deep 进程内编排，exploitation 是临时 sub-agent，不单独建 hunter 行，
//     故 orchestrator_id 多为 NULL。该列保留供 viewer 按树渲染 + 向后兼容旧 subtask swarm 数据。
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
// OrchestratorID 空表示独立/根任务；非空时指向 orchestrator hunter.id（旧 subtask swarm 数据）。
type Run struct {
	ID             string
	OwnerType      string // 'passive_session' 或 'active_scan'
	OwnerID        string // passive_session.id / active_scan.id
	OrchestratorID string
	Role           string
	Input          json.RawMessage
	Result         json.RawMessage
	Status         Status
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewParams 是 Store.Create 的入参。
// OrchestratorID 留空表示独立/根任务；填值时 INSERT 写入 orchestrator_id 列。
// OwnerType + OwnerID 必填（0041 之后 NOT NULL）。
type NewParams struct {
	OwnerType      string
	OwnerID        string
	Role           string
	Input          json.RawMessage
	OrchestratorID string
}
