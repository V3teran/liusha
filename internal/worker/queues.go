// Package worker 封装 asynq 任务队列：
// 提供 Client（生产者）+ Mux（消费者路由），按 Role 路由到 sniffer / operator 队列。
package worker

// Role 表示一个任务由哪种 agent 执行。
type Role string

const (
	// RoleSniffer 表示侦察类任务（spec §3.2 主链路，高优先级）。
	RoleSniffer Role = "sniffer"
	// RoleOperator 表示执行类子任务（spec §3.3 子工作流，低优先级）。
	RoleOperator Role = "operator"
)

// 队列名（asynq Queue），priority 在消费端 Server.Config.Queues 配置：sniffer=5, operator=1。
const (
	QueueSniffer  = "agent:sniffer"
	QueueOperator = "agent:operator"
)

// 单一 task type，路由信息放在 Payload.Role 里，避免 type 字符串爆炸。
const TaskTypeRun = "agent:run"

// Queue 把 Role 映射到对应的 asynq queue 名。未识别的 role 默认走 sniffer。
func (r Role) Queue() string {
	if r == RoleOperator {
		return QueueOperator
	}
	return QueueSniffer
}
