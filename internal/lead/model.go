// Package lead 实现情报黑板：assignment 级别的跨 task 情报共享。
//
// 定位：同一 assignment 下的多个 task 共享情报黑板，子代理通过黑板同步过程情报。
//
// 隔离维度：assignment_id（租户隔离，同一批测试共享）
// 存储：PostgreSQL（持久化，废弃 Redis）
package lead

import "time"

// Kind 是情报能触发的 agent 动作分类——按动作分（最少且穷尽），不按主题分。
type Kind string

const (
	KindClue        Kind = "clue"        // 可疑点 → 去验证
	KindObservation Kind = "observation" // 既成发现 → 记住并继续
	KindDeadend     Kind = "deadend"     // 死路 → 绕开别试
)

// Entry 是一条情报。
//
// SourceTaskID：哪次 task 发现的（溯源，前端展示"来自哪次扫描"）。
// lead(observation) 与 finding 的边界：finding = 有 evidence、可复现 repro_cmd、进交付报告的漏洞；
// lead(observation) = 尚未坐实成 PoC 的认知/观察。升级路径：验证成 PoC → 写 finding；lead 保留在黑板。
type Entry struct {
	Kind         Kind      `json:"kind"`
	Detail       string    `json:"detail"` // 一句人话，位置/细节都在这里说清
	ExecutorID   string    `json:"executor_id"`
	SourceTaskID string    `json:"source_task_id"`
	CreatedAt    time.Time `json:"created_at"`
}
