// Package agentrun 实现 agent_run 表持久化层：每次 Agent 运行实例对应一行。
// 状态机 pending → running → completed | error | aborted。
//
// 命名层级：
//   - task = 业务"扫描任务"（用户视角的 task；来源 direct/proxy 记在所属 assignment 上）
//   - agent_run 表 = 一次执行的记录，挂 task_id：一次 run 是整个四 agent 认知循环，
//     四个 agent 共享该行（LLM / 工具调用、流量、坐实漏洞都按它归属）
package agentrun

import "time"

// Status 是 agent.status 的取值。
type Status string

// agent_run 状态机：pending → running → completed/aborted/error。
const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusCompleted Status = "completed"
	StatusAborted   Status = "aborted"
	StatusError     Status = "error"
)

// Run 是 agent_run 表行的 Go 表示——一次执行的状态快照。结局的原因不在这里：任务终止
// 原因记在任务行（task.abort_reason），停止原因记在时间线终态节点，出错细节记日志。
type Run struct {
	ID        string
	TaskID    string // 所属 task.id
	Status    Status
	CreatedAt time.Time
	UpdatedAt time.Time
}

// NewParams 是 Store.Create 的入参。
// TaskID 必填（NOT NULL 外键）。
type NewParams struct {
	TaskID string
}
