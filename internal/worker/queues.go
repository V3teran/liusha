// Package worker 封装 asynq 任务队列：
// 提供 Client（生产者）+ Mux（消费者路由），按 Role 路由到 executor / dispatch 队列。
package worker

// Role 表示 asynq queue 路由 enum——决定一个任务入哪个 redis queue。
//
// 这里的 Role 是 **asynq 调度层** 的标识，**不是** executor agent 内部角色。
// agent run 的三种角色（traffic-analysis / planner / exploitation，见 agent.role
// CHECK 约束）都走同一个 RoleExecutor queue；具体 agent 角色记录在 agent.role 列
// （cmd/api / ingestor / active_spawner 创建 agent run 时按 mode + isParent 写入），
// 跟 asynq 路由解耦。
//
//   - RoleExecutor   = 所有 agent task 走的统一 queue
//   - RoleDispatch = 备用调度队列（v1.5+ 优先级或专属队列）
type Role string

const (
	// RoleExecutor agent 任务统一 queue（agent 小队的全部 agent task）。
	RoleExecutor Role = "executor"
	// RoleDispatch 备用调度队列（v1.5+ 优先级或专属队列）。
	RoleDispatch Role = "dispatch"
)

// 队列名（asynq Queue），priority 在消费端 Server.Config.Queues 配置。
const (
	QueueExecutor = "executor"
	QueueDispatch = "dispatch"
)

// TaskTypeRun 是单一 asynq task type（按 Payload.Role 在 handler 内部再分发）。
const TaskTypeRun = "agent.run"

// Queue 把 Role 映射到对应的 asynq queue 名。
func (r Role) Queue() string {
	if r == RoleDispatch {
		return QueueDispatch
	}
	return QueueExecutor
}
