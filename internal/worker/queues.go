// Package worker 封装 asynq 任务队列：
// 提供 Client（生产者）+ Mux（消费者路由），统一走 executor 队列。
package worker

// TaskRole 表示 asynq 队道路由 enum——决定一个任务入哪个 redis queue。
//
// 这里的 TaskRole 是 **asynq 调度层** 的标识，**不是** 认知循环里的 agent 角色。
// agent_run 的四种角色（planner/executor/evaluator/monitor，见 agent_run.role
// CHECK 约束）都走同一个 RoleExecutor queue；agent 角色记录在 agent_run.role 列，
// 跟 asynq 路由解耦。
type TaskRole string

const (
	// RoleExecutor 所有 agent 运行走的统一 queue。
	RoleExecutor TaskRole = "executor"
)

// QueueExecutor 是唯一的 asynq 队列名；priority 在消费端 Server.Config.Queues 配置。
const QueueExecutor = "executor"

// TaskTypeRun 是单一 asynq task type（按 Payload.TaskRole 在 handler 内部再分发）。
const TaskTypeRun = "agent.run"

// Queue 把 TaskRole 映射到对应的 asynq queue 名。
func (r TaskRole) Queue() string {
	return QueueExecutor
}
