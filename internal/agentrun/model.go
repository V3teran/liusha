// Package agentrun 实现 executor 表持久化层：每次 executor ReAct 运行对应一行。
// 状态机 pending → running → done | error | aborted。
//
// 命名层级：
//   - task = 业务"扫描任务"（用户视角的 task，mode 区分 active/passive）
//   - agent 表 = 每次 agent ReAct 运行的记录（planner 或 exploitation），挂 task_id
//   - role 列 = trafficAnalysis / planner / exploitation
//
// planner_id 列（任务树关系）：
//   - planner / 独立任务：planner_id = NULL
//   - dispatcher 编排路径下 exploitation 是进程内临时 Actor，不单独建 executor 行，
//     故 planner_id 多为 NULL。该列保留供前端按树渲染。
package agentrun

import (
	"encoding/json"
	"time"
)

// Status 是 agent.status 的取值。
type Status string

const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusAborted Status = "aborted"
	StatusError   Status = "error"
)

// Run 是 agent 表行的 Go 表示——每次 agent ReAct 运行的状态快照。
// Result 在终态前为空 jsonb '{}'。
// plannerID 空表示独立/根任务；非空时指向 planner agent.id（旧 subtask swarm 数据）。
type Run struct {
	ID             string
	TaskID         string // 所属 task.id
	plannerID string
	Role           string
	Input          json.RawMessage
	Result         json.RawMessage
	Status         Status
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// NewParams 是 Store.Create 的入参。
// plannerID 留空表示独立/根任务；填值时 INSERT 写入 planner_id 列。
// TaskID 必填（NOT NULL 外键）。
type NewParams struct {
	TaskID         string
	Role           string
	Input          json.RawMessage
	plannerID string
}
