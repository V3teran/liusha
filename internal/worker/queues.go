// Package worker 封装 asynq 任务队列：
// 提供 Client（生产者）+ Mux（消费者路由），按 Role 路由到 hunter / dispatch 队列。
package worker

// Role 表示一个任务由哪种 agent 执行。
//
// v0024 agentic-lean：单层 hunter agent 架构。
//   - hunter   = 漏洞挖掘单一 agent（接 1 条流量，自由组合工具挖漏洞）
//   - dispatch = 备用调度队列（v1.5+ 优先级或专属队列）
type Role string

const (
	// RoleHunter 漏洞挖掘 agent 任务（v0024 单层架构）。
	RoleHunter Role = "hunter"
	// RoleDispatch 备用调度队列（v1.5+ 优先级或专属队列）。
	RoleDispatch Role = "dispatch"
)

// 队列名（asynq Queue），priority 在消费端 Server.Config.Queues 配置。
const (
	QueueHunter   = "hunter"
	QueueDispatch = "dispatch"
)

// TaskTypeRun 是单一 asynq task type（按 Payload.Role 在 handler 内部再分发）。
const TaskTypeRun = "agent.run"

// Queue 把 Role 映射到对应的 asynq queue 名。
func (r Role) Queue() string {
	if r == RoleDispatch {
		return QueueDispatch
	}
	return QueueHunter
}
