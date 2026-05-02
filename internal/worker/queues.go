// Package worker 封装 asynq 任务队列：
// 提供 Client（生产者）+ Mux（消费者路由），按 Role 路由到 main / dispatch 队列。
package worker

// Role 表示一个任务由哪种 agent 执行。
//
// 命名约定（v1.1 重命名）：
//   - orchestrator = 主 ReAct 协调者（分类流量 + 派发 hunter skill + 收尾），不亲自做漏洞探测
//   - dispatch     = 备用调度队列（v1.5+ 优先级或专属队列）
type Role string

const (
	// RoleOrchestrator 主 ReAct 协调任务：分类流量、派发 hunter skill、收尾。
	RoleOrchestrator Role = "orchestrator"
	// RoleDispatch 备用调度队列（v1.5+ 优先级或专属队列）。
	RoleDispatch Role = "dispatch"
)

// 队列名（asynq Queue），priority 在消费端 Server.Config.Queues 配置。
// 所有 liusha 项目的 redis key 统一加 `liusha:` 前缀，便于单 redis 实例多项目共享。
const (
	QueueOrchestrator = "liusha:orchestrator"
	QueueDispatch     = "liusha:dispatch"
)

const TaskTypeRun = "liusha:run"

// Queue 把 Role 映射到对应的 asynq queue 名。
func (r Role) Queue() string {
	if r == RoleDispatch {
		return QueueDispatch
	}
	return QueueOrchestrator
}
