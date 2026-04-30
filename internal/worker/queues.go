// Package worker 封装 asynq 任务队列：
// 提供 Client（生产者）+ Mux（消费者路由），按 Role 路由到 main / dispatch 队列。
package worker

// Role 表示一个任务由哪种 agent 执行。
type Role string

const (
	// RoleMain 主 ReAct 任务（含 traffic / site 模式）。
	RoleMain Role = "main"
	// RoleDispatch 备用调度队列（v1.5+ 优先级或专属队列）。
	RoleDispatch Role = "dispatch"
)

// 队列名（asynq Queue），priority 在消费端 Server.Config.Queues 配置。
const (
	QueueMain     = "agent:react"
	QueueDispatch = "agent:dispatch"
)

const TaskTypeRun = "agent:run"

// Queue 把 Role 映射到对应的 asynq queue 名。
func (r Role) Queue() string {
	if r == RoleDispatch {
		return QueueDispatch
	}
	return QueueMain
}
