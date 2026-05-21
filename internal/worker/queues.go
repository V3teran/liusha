// Package worker 封装 asynq 任务队列：
// 提供 Client（生产者）+ Mux（消费者路由），按 Role 路由到 hunter / dispatch 队列。
package worker

// Role 表示 asynq queue 路由 enum——决定一个任务入哪个 redis queue。
//
// 这里的 Role 是 **asynq 调度层** 的标识，**不是** hunter agent 内部角色。
// hunter 小队下的四种角色（tracker / commander / striker / inspector）
// 都走同一个 RoleHunter queue；具体 agent 角色记录在 hunter.role 列
// （cmd/api / ingestor / active_spawner 创建 hunter run 时按 mode + isParent 写入），
// 跟 asynq 路由解耦。
//
//   - RoleHunter   = 所有 hunter task 走的统一 queue
//   - RoleDispatch = 备用调度队列（v1.5+ 优先级或专属队列）
type Role string

const (
	// RoleHunter hunter 任务统一 queue（hunter 小队的全部 agent task）。
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
