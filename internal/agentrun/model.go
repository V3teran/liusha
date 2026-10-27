// Package agentrun 实现 agent_run 表持久化层：每次 Agent 运行实例对应一行。
// 状态机 pending → running → done | error | aborted。
//
// 命名层级：
//   - task = 业务"扫描任务"（用户视角的 task，mode 区分 active/passive）
//   - agent_run 表 = 每次 Agent 运行实例的记录（planner/executor/evaluator/monitor），挂 task_id
//   - agent 表 = Agent 配置定义（code/name/body/tools），存储 Agent 章程
//   - role 列 = 发起该 run 的 Agent 角色。当前仅 planner 建 run（一次 run =
//     整个四 agent 认知循环，executor/evaluator/monitor 共享该行）；列保留
//     四值枚举为按角色拆 run 留演进位。
package agentrun

import (
	"encoding/json"
	"time"
)

// Status 是 agent.status 的取值。
type Status string

// agent_run 状态机：pending → running → done/aborted/error。
const (
	StatusPending Status = "pending"
	StatusRunning Status = "running"
	StatusDone    Status = "done"
	StatusAborted Status = "aborted"
	StatusError   Status = "error"
)

// Run 是 agent_run 表行的 Go 表示——每次 Agent 运行实例的状态快照。
// Result 在终态前为空 jsonb '{}'。
type Run struct {
	ID        string
	TaskID    string // 所属 task.id
	Role      string
	Input     json.RawMessage
	Result    json.RawMessage
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewParams 是 Store.Create 的入参。
// TaskID 必填（NOT NULL 外键）。
type NewParams struct {
	TaskID string
	Role   string
	Input  json.RawMessage
}
